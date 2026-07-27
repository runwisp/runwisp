// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/runwisp/runwisp/internal/server"
)

// NotificationStreamEvent is an SSEEvent from /api/notifications/stream.
type NotificationStreamEvent = SSEEvent

// ListNotifications fetches one page of notifications. Pass before="" for the
// most recent page; subsequent pages use NextCursor from the previous response.
func (c *Client) ListNotifications(limit int, before string) (server.NotificationsListBody, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		q.Set("before", before)
	}
	path := "/api/notifications"
	if encoded := q.Encode(); encoded != "" {
		path = path + "?" + encoded
	}
	var page server.NotificationsListBody
	if err := c.doJSON("GET", path, nil, &page); err != nil {
		return server.NotificationsListBody{}, err
	}
	return page, nil
}

// MarkAllNotificationsRead stamps every currently-unread row as read using the
// server's clock.
func (c *Client) MarkAllNotificationsRead() error {
	return c.doJSON("POST", "/api/notifications/read", nil, nil)
}

// MarkNotificationRead stamps a single notification as read.
func (c *Client) MarkNotificationRead(id string) error {
	return c.doJSON("POST", "/api/notifications/"+url.PathEscape(id)+"/read", nil, nil)
}

// MarkNotificationUnread clears the read marker on a single notification so it
// re-enters the unread set.
func (c *Client) MarkNotificationUnread(id string) error {
	return c.doJSON("POST", "/api/notifications/"+url.PathEscape(id)+"/unread", nil, nil)
}

// UnreadNotificationCount returns the number of notifications with read_at IS
// NULL.
func (c *Client) UnreadNotificationCount() (int64, error) {
	var resp struct {
		Count int64 `json:"count"`
	}
	if err := c.doJSON("GET", "/api/notifications/unread-count", nil, &resp); err != nil {
		return 0, err
	}
	return resp.Count, nil
}

// StreamNotifications opens an SSE connection to /api/notifications/stream and
// delivers parsed events. The channel is closed when ctx is cancelled or the
// underlying stream ends.
func (c *Client) StreamNotifications(ctx context.Context) (<-chan NotificationStreamEvent, error) {
	resp, err := c.doSSE(ctx, "/api/notifications/stream")
	if err != nil {
		return nil, err
	}
	ch := make(chan NotificationStreamEvent, 32)
	go simpleSSELoop(ctx, resp.Body, ch)
	return ch, nil
}

// DecodeNotificationEnvelope unwraps a notification.created or
// notification.updated event's payload, returning the row and the server's
// post-mutation unread count. Both event types share the same JSON shape, so
// server.NotificationCreatedEvent is used as the decode target for both.
func DecodeNotificationEnvelope(data []byte) (server.NotificationCreatedEvent, error) {
	var env server.NotificationCreatedEvent
	if err := json.Unmarshal(data, &env); err != nil {
		return server.NotificationCreatedEvent{}, fmt.Errorf("decode notification: %w", err)
	}
	return env, nil
}

// DecodeUnreadCountEnvelope unwraps a notifications.unread_count_changed
// event's payload to a single int64.
func DecodeUnreadCountEnvelope(data []byte) (int64, error) {
	var env server.NotificationUnreadCountEvent
	if err := json.Unmarshal(data, &env); err != nil {
		return 0, fmt.Errorf("decode unread count: %w", err)
	}
	return env.UnreadCount, nil
}
