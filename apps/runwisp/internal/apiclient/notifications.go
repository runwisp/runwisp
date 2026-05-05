// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Notification mirrors the server's NotificationDTO. ISO timestamps from the
// server are decoded as time.Time on this side; the Occurrences ring stays
// as raw RFC3339Nano strings since the TUI/web UI both render them
// directly. ReadAt is nil when the row is unread.
type Notification struct {
	ID             string     `json:"id"`
	Fingerprint    string     `json:"fingerprint"`
	Kind           string     `json:"kind"`
	Severity       string     `json:"severity"`
	TaskName       string     `json:"task_name"`
	RunID          string     `json:"run_id"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	Count          int        `json:"count"`
	Occurrences    []string   `json:"occurrences"`
	CreatedAt      time.Time  `json:"created_at"`
	LastOccurredAt time.Time  `json:"last_occurred_at"`
	ReadAt         *time.Time `json:"read_at,omitempty"`
}

// NotificationsPage is the cursor-paginated list response.
type NotificationsPage struct {
	Items      []Notification `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// NotificationStreamEvent is a parsed SSE event from /api/notifications/stream.
// Type is one of "notification.created", "notification.updated", or "ping".
type NotificationStreamEvent struct {
	Type string
	Data json.RawMessage
}

// NotificationEnvelope unwraps the {notification: {...}} payload that
// notification.created and notification.updated use.
type NotificationEnvelope struct {
	Notification Notification `json:"notification"`
}

// ListNotifications fetches one page of notifications. Pass before="" for the
// most recent page; subsequent pages use NextCursor from the previous response.
func (c *Client) ListNotifications(limit int, before string) (NotificationsPage, error) {
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
	var page NotificationsPage
	if err := c.doJSON("GET", path, nil, &page); err != nil {
		return NotificationsPage{}, err
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
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		var eventType string

		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, "event: ") {
				eventType = strings.TrimPrefix(line, "event: ")
				continue
			}

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if eventType != "" {
					select {
					case ch <- NotificationStreamEvent{
						Type: eventType,
						Data: json.RawMessage(data),
					}:
					case <-ctx.Done():
						return
					}
					eventType = ""
				}
				continue
			}

			if line == "" {
				eventType = ""
			}
		}
	}()
	return ch, nil
}

// DecodeNotificationEnvelope unwraps a notification.created or
// notification.updated event's payload to a Notification.
func DecodeNotificationEnvelope(data []byte) (Notification, error) {
	var env NotificationEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Notification{}, fmt.Errorf("decode notification: %w", err)
	}
	return env.Notification, nil
}
