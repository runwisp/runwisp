// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
}

// NotificationDTO is the JSON shape we expose. The storage type embeds
// time.Time + a slice; this maps to ISO8601 strings that the TS client can
// parse directly.
type NotificationDTO struct {
	ID             string    `json:"id" doc:"Stable ULID identifier"`
	Fingerprint    string    `json:"fingerprint" doc:"Coalescing key (FNV1a hex)"`
	Kind           string    `json:"kind" doc:"Event kind (run.failed, notify.delivery_failed, ...)"`
	Severity       string    `json:"severity" doc:"info | warn | error"`
	TaskName       string    `json:"task_name" doc:"Task that produced this notification (empty for daemon-level events)"`
	RunID          string    `json:"run_id" doc:"Run that produced this notification (empty when not run-derived)"`
	Title          string    `json:"title" doc:"Human-readable title"`
	Body           string    `json:"body" doc:"Pre-rendered body text"`
	Count          int       `json:"count" doc:"Number of coalesced occurrences within the window"`
	Occurrences    []string  `json:"occurrences" doc:"Most-recent timestamps (newest first), ISO8601"`
	CreatedAt      time.Time `json:"created_at" doc:"First time this notification was raised"`
	LastOccurredAt time.Time `json:"last_occurred_at" doc:"Most recent occurrence"`
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
	NextCursor string            `json:"next_cursor,omitempty" doc:"Cursor to pass as 'before' on the next page; empty when exhausted"`
}

type NotificationMarkReadInput struct {
	Body NotificationMarkReadBody
}

type NotificationMarkReadBody struct {
	LastReadAt time.Time `json:"last_read_at" doc:"Mark all notifications with last_occurred_at <= this as read" required:"true"`
}

type NotificationUnreadOutput struct {
	Body NotificationUnreadBody
}

type NotificationUnreadBody struct {
	Count      int64     `json:"count" doc:"Number of notifications strictly newer than last_read_at"`
	LastReadAt time.Time `json:"last_read_at" doc:"Server-side last-read marker (zero when never set)"`
}

// ---------- SSE wrapper types ----------
// huma/sse dispatches event names by Go type, so the created/updated payloads
// each need a distinct named type. Both wrap the same DTO.

type NotificationCreatedEvent struct {
	Notification NotificationDTO `json:"notification"`
}

type NotificationUpdatedEvent struct {
	Notification NotificationDTO `json:"notification"`
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
		OperationID:   "markNotificationsRead",
		Method:        http.MethodPost,
		Path:          "/api/notifications/read",
		Summary:       "Set the last-read marker",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaMarkNotificationsRead)

	huma.Register(api, huma.Operation{
		OperationID: "getUnreadNotificationCount",
		Method:      http.MethodGet,
		Path:        "/api/notifications/unread-count",
		Summary:     "Count notifications newer than the last-read marker",
		Tags:        []string{"Notifications"},
	}, srv.humaUnreadNotificationCount)

	srv.registerNotificationsSSE(api)
}

// ---------- Handlers ----------

func (srv *Server) humaListNotifications(_ context.Context, input *NotificationsListInput) (*NotificationsListOutput, error) {
	rows, err := srv.notifyRepo.ListNotifications(input.Limit, input.Before)
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

func (srv *Server) humaMarkNotificationsRead(_ context.Context, input *NotificationMarkReadInput) (*struct{}, error) {
	if err := srv.notifyRepo.SetLastReadAt(input.Body.LastReadAt); err != nil {
		return nil, huma.Error500InternalServerError("Failed to set read marker")
	}
	return nil, nil
}

func (srv *Server) humaUnreadNotificationCount(_ context.Context, _ *struct{}) (*NotificationUnreadOutput, error) {
	last, err := srv.notifyRepo.GetLastReadAt()
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to read marker")
	}
	count, err := srv.notifyRepo.CountNotificationsSince(last)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to count notifications")
	}
	out := &NotificationUnreadOutput{}
	out.Body.Count = count
	out.Body.LastReadAt = last
	return out, nil
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
		Description: "Server-Sent Events stream emitting notification.created and notification.updated as in-app rows are coalesced.",
		Tags:        []string{"Notifications"},
	}, map[string]any{
		"notification.created": NotificationCreatedEvent{},
		"notification.updated": NotificationUpdatedEvent{},
		"ping":                 PingEvent{},
	}, func(ctx context.Context, _ *struct{}, send sse.Sender) {
		release, ok := srv.streams.acquire(streamClientIPFromCtx(ctx))
		if !ok {
			return
		}
		defer release()

		if err := send(sse.Message{Data: PingEvent{}}); err != nil {
			return
		}

		if srv.notifyHub == nil {
			// Notifications disabled — keep the stream alive with pings so the
			// browser EventSource doesn't error-loop, but emit nothing else.
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := send(sse.Message{Data: PingEvent{}}); err != nil {
						return
					}
				}
			}
		}

		sub, unsubscribe := srv.notifyHub.Subscribe()
		defer unsubscribe()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case u, ok := <-sub.Channel():
				if !ok {
					return
				}
				dto := notificationToDTO(u.Notification)
				var payload any
				switch u.Type {
				case "notification.created":
					payload = NotificationCreatedEvent{Notification: dto}
				case "notification.updated":
					payload = NotificationUpdatedEvent{Notification: dto}
				default:
					payload = NotificationUpdatedEvent{Notification: dto}
				}
				if err := send(sse.Message{Data: payload}); err != nil {
					return
				}
			case <-ticker.C:
				if err := send(sse.Message{Data: PingEvent{}}); err != nil {
					return
				}
			}
		}
	})
}
