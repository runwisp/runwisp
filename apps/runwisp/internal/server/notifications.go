// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/runwisp/runwisp/internal/storage"
)

// NotificationHub is the interface the server requires from the in-app
// notification hub. The concrete implementation lives in
// internal/notify/channel/inapp; the server depends on the interface so it
// can be tested with a fake.
type NotificationHub interface {
	Subscribe() (*inapp.Subscriber, func())
	Publish(u inapp.Update)
}

// NotificationDTO is the JSON shape we expose. The storage type embeds
// time.Time + a slice; this maps to ISO8601 strings that the TS client can
// parse directly.
type NotificationDTO struct {
	ID             string     `json:"id" doc:"Stable ULID identifier"`
	Fingerprint    string     `json:"fingerprint" doc:"Coalescing key (FNV1a hex)"`
	Kind           string     `json:"kind" doc:"Event kind (run.failed, notify.delivery_failed, ...)"`
	Severity       string     `json:"severity" doc:"info | warn | error"`
	TaskName       string     `json:"taskName" doc:"Task that produced this notification (empty for daemon-level events)"`
	RunID          string     `json:"runId" doc:"Run that produced this notification (empty when not run-derived)"`
	Title          string     `json:"title" doc:"Human-readable title"`
	Body           string     `json:"body" doc:"Pre-rendered body text"`
	Count          int        `json:"count" doc:"Number of coalesced occurrences within the window"`
	Occurrences    []string   `json:"occurrences" doc:"Most-recent timestamps (newest first), ISO8601"`
	CreatedAt      time.Time  `json:"createdAt" doc:"First time this notification was raised"`
	LastOccurredAt time.Time  `json:"lastOccurredAt" doc:"Most recent occurrence"`
	ReadAt         *time.Time `json:"readAt,omitempty" doc:"When the operator marked this row read; null/absent when unread"`
}

func notificationToDTO(n storage.Notification) NotificationDTO {
	occ := make([]string, len(n.Occurrences))
	for i, t := range n.Occurrences {
		occ[i] = t.Format(time.RFC3339Nano)
	}
	return NotificationDTO{
		ID:             n.ID,
		Fingerprint:    n.Fingerprint,
		Kind:           n.Kind,
		Severity:       n.Severity,
		TaskName:       n.TaskName,
		RunID:          n.RunID,
		Title:          n.Title,
		Body:           n.Body,
		Count:          n.Count,
		Occurrences:    occ,
		CreatedAt:      n.CreatedAt,
		LastOccurredAt: n.LastOccurredAt,
		ReadAt:         n.ReadAt,
	}
}

// ---------- REST inputs / outputs ----------

type NotificationsListInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"500" default:"50" doc:"Max items per page"`
	Before string `query:"before" doc:"Cursor: return only items with id < before (descending)"`
}

type NotificationsListOutput struct {
	Body NotificationsListBody
}

type NotificationsListBody struct {
	Items      []NotificationDTO `json:"items" doc:"Notifications in id-DESC order"`
	NextCursor string            `json:"nextCursor,omitempty" doc:"Cursor to pass as 'before' on the next page; empty when exhausted"`
}

type NotificationUnreadOutput struct {
	Body NotificationUnreadBody
}

type NotificationUnreadBody struct {
	Count int64 `json:"count" doc:"Number of notifications with read_at IS NULL"`
}

type NotificationByIDInput struct {
	ID string `path:"notificationId" doc:"Notification ULID"`
}

// ---------- SSE wrapper types ----------
// huma/sse dispatches event names by Go type, so the created/updated payloads
// each need a distinct named type. UnreadCount is the post-mutation count of
// rows with read_at IS NULL — clients use it as the authoritative badge value
// instead of delta-tracking. A negative value means the server failed to
// query and the client should ignore this field.

type NotificationCreatedEvent struct {
	Notification NotificationDTO `json:"notification"`
	UnreadCount  int64           `json:"unreadCount"`
}

type NotificationUpdatedEvent struct {
	Notification NotificationDTO `json:"notification"`
	UnreadCount  int64           `json:"unreadCount"`
}

// NotificationUnreadCountEvent is the body-less count-only event emitted when
// a mutation changes the unread count without a single notification to ship
// (e.g. mark-all-read).
type NotificationUnreadCountEvent struct {
	UnreadCount int64 `json:"unreadCount"`
}

// ---------- Registration ----------

// registerNotificationsRoutes registers the REST + SSE shapes. The repository
// is wired unconditionally so REST handlers always succeed; the SSE handler
// degrades to a ping-only stream when the in-app Hub is absent (notify
// disabled).
func (srv *Server) registerNotificationsRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listNotifications",
		Method:      http.MethodGet,
		Path:        "/api/notifications",
		Summary:     "List in-app notifications",
		Tags:        []string{"Notifications"},
	}, srv.humaListNotifications)

	huma.Register(api, huma.Operation{
		OperationID:   "markAllNotificationsRead",
		Method:        http.MethodPost,
		Path:          "/api/notifications/read",
		Summary:       "Mark every unread notification read",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaMarkAllNotificationsRead)

	huma.Register(api, huma.Operation{
		OperationID:   "markNotificationRead",
		Method:        http.MethodPost,
		Path:          "/api/notifications/{notificationId}/read",
		Summary:       "Mark a single notification read",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaMarkNotificationRead)

	huma.Register(api, huma.Operation{
		OperationID:   "markNotificationUnread",
		Method:        http.MethodPost,
		Path:          "/api/notifications/{notificationId}/unread",
		Summary:       "Mark a single notification unread",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaMarkNotificationUnread)

	huma.Register(api, huma.Operation{
		OperationID: "getUnreadNotificationCount",
		Method:      http.MethodGet,
		Path:        "/api/notifications/unreadCount",
		Summary:     "Count notifications with read_at IS NULL",
		Tags:        []string{"Notifications"},
	}, srv.humaUnreadNotificationCount)

	srv.registerNotificationsSSE(api)
}

// ---------- Handlers ----------

func (srv *Server) humaListNotifications(ctx context.Context, input *NotificationsListInput) (*NotificationsListOutput, error) {
	rows, err := srv.notifyRepo.ListNotifications(ctx, input.Limit, input.Before)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to list notifications")
	}
	items := make([]NotificationDTO, len(rows))
	for i, r := range rows {
		items[i] = notificationToDTO(r)
	}
	out := &NotificationsListOutput{}
	out.Body.Items = items
	if len(rows) == input.Limit && len(rows) > 0 {
		out.Body.NextCursor = rows[len(rows)-1].ID
	}
	return out, nil
}

func (srv *Server) humaMarkAllNotificationsRead(ctx context.Context, _ *struct{}) (*struct{}, error) {
	if err := srv.notifyRepo.MarkAllNotificationsRead(ctx, time.Now()); err != nil {
		return nil, huma.Error500InternalServerError("Failed to mark notifications read")
	}
	// Re-query rather than assume 0: a notification created concurrently with
	// this mutation (after the UPDATE committed) is still unread, and other
	// subscribers need the real count, not a hardcoded value that ignores it.
	count, err := srv.notifyRepo.CountUnreadNotifications(ctx)
	if err != nil {
		count = -1
	}
	srv.publishUnreadCountChanged(count)
	return nil, nil
}

func (srv *Server) humaMarkNotificationRead(ctx context.Context, input *NotificationByIDInput) (*struct{}, error) {
	updated, err := srv.notifyRepo.MarkNotificationRead(ctx, input.ID, time.Now())
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to mark notification read")
	}
	srv.publishNotificationUpdate(ctx, updated)
	return nil, nil
}

func (srv *Server) humaMarkNotificationUnread(ctx context.Context, input *NotificationByIDInput) (*struct{}, error) {
	updated, err := srv.notifyRepo.MarkNotificationUnread(ctx, input.ID)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to mark notification unread")
	}
	srv.publishNotificationUpdate(ctx, updated)
	return nil, nil
}

func (srv *Server) humaUnreadNotificationCount(ctx context.Context, _ *struct{}) (*NotificationUnreadOutput, error) {
	count, err := srv.notifyRepo.CountUnreadNotifications(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to count notifications")
	}
	out := &NotificationUnreadOutput{}
	out.Body.Count = count
	return out, nil
}

// publishNotificationUpdate fans a read-state change out to live SSE
// subscribers so other surfaces (Web UI, second TUI window) stay in sync.
// The post-mutation unread count is queried once and shipped on the event so
// clients never have to delta-track.
func (srv *Server) publishNotificationUpdate(ctx context.Context, n *storage.Notification) {
	if srv.notifyHub == nil || n == nil {
		return
	}
	count, err := srv.notifyRepo.CountUnreadNotifications(ctx)
	if err != nil {
		// Ship the row update even if the count query failed; -1 tells the
		// client to ignore the count field rather than trust a stale value.
		count = -1
	}
	srv.notifyHub.Publish(inapp.Update{Type: inapp.UpdateTypeUpdated, Notification: *n, UnreadCount: count})
}

// publishUnreadCountChanged fans a count-only update (e.g. mark-all-read) so
// SSE clients can refresh their badge without a per-row event burst.
func (srv *Server) publishUnreadCountChanged(count int64) {
	if srv.notifyHub == nil {
		return
	}
	srv.notifyHub.Publish(inapp.Update{Type: inapp.UpdateTypeUnreadCountChanged, UnreadCount: count})
}

// registerNotificationsSSE mirrors the runs SSE stream: bounded per-conn
// buffer, drop-oldest on backpressure, 30s pings to keep the stream alive
// through proxies. Hub publishes lightweight Updates that we re-shape into
// the JSON DTO clients consume.
func (srv *Server) registerNotificationsSSE(api huma.API) {
	sse.Register(api, huma.Operation{
		OperationID: "streamNotifications",
		Method:      http.MethodGet,
		Path:        "/api/notifications/stream",
		Summary:     "Stream notification create/update events",
		Description: "Server-Sent Events stream emitting notification.created and notification.updated as in-app rows are coalesced or marked read/unread.",
		Tags:        []string{"Notifications"},
	}, map[string]any{
		inapp.UpdateTypeCreated:            NotificationCreatedEvent{},
		inapp.UpdateTypeUpdated:            NotificationUpdatedEvent{},
		inapp.UpdateTypeUnreadCountChanged: NotificationUnreadCountEvent{},
		"ping":                             PingEvent{},
	}, srv.sseNotificationsHandler)
}

func (srv *Server) sseNotificationsHandler(ctx context.Context, _ *struct{}, send sse.Sender) {
	release, ok := srv.streams.acquire(ctx)
	if !ok {
		return
	}
	defer release()

	if err := send(sse.Message{Data: PingEvent{}}); err != nil {
		return
	}

	// notifyCh stays nil when notify is disabled — that select arm then simply
	// never fires, so the loop degrades to ping-only and keeps the browser
	// EventSource from error-looping.
	var notifyCh <-chan inapp.Update
	if srv.notifyHub != nil {
		sub, unsubscribe := srv.notifyHub.Subscribe()
		defer unsubscribe()
		notifyCh = sub.Channel()
	}

	sseNotificationsLoop(ctx, notifyCh, send)
}

func notifyUpdateToPayload(u inapp.Update) any {
	switch u.Type {
	case inapp.UpdateTypeCreated:
		return NotificationCreatedEvent{Notification: notificationToDTO(u.Notification), UnreadCount: u.UnreadCount}
	case inapp.UpdateTypeUpdated:
		return NotificationUpdatedEvent{Notification: notificationToDTO(u.Notification), UnreadCount: u.UnreadCount}
	case inapp.UpdateTypeUnreadCountChanged:
		return NotificationUnreadCountEvent{UnreadCount: u.UnreadCount}
	default:
		return NotificationUpdatedEvent{Notification: notificationToDTO(u.Notification), UnreadCount: u.UnreadCount}
	}
}

func sseNotificationsLoop(ctx context.Context, notifyCh <-chan inapp.Update, send sse.Sender) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-notifyCh:
			if !ok {
				notifyCh = nil
				continue
			}
			if err := send(sse.Message{Data: notifyUpdateToPayload(u)}); err != nil {
				return
			}
		case <-ticker.C:
			if err := send(sse.Message{Data: PingEvent{}}); err != nil {
				return
			}
		}
	}
}
