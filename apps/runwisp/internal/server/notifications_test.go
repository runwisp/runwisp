// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockNotificationRepository struct {
	mock.Mock
}

func (m *mockNotificationRepository) UpsertByFingerprint(n *storage.Notification, window time.Duration, ringSize int) (bool, error) {
	args := m.Called(n, window, ringSize)
	return args.Bool(0), args.Error(1)
}

func (m *mockNotificationRepository) ListNotifications(limit int, before string) ([]storage.Notification, error) {
	args := m.Called(limit, before)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) GetNotificationByID(id string) (*storage.Notification, error) {
	args := m.MethodCalled("GetNotificationByID", id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) PruneNotificationsByCount(keep int) (int64, error) {
	args := m.MethodCalled("PruneNotificationsByCount", keep)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockNotificationRepository) PruneNotificationsByAge(olderThan time.Duration) (int64, error) {
	args := m.MethodCalled("PruneNotificationsByAge", olderThan)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockNotificationRepository) CountUnreadNotifications() (int64, error) {
	args := m.MethodCalled("CountUnreadNotifications")
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockNotificationRepository) MarkNotificationRead(id string, at time.Time) (*storage.Notification, error) {
	args := m.MethodCalled("MarkNotificationRead", id, at)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) MarkNotificationUnread(id string) (*storage.Notification, error) {
	args := m.MethodCalled("MarkNotificationUnread", id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) MarkAllNotificationsRead(at time.Time) error {
	args := m.Called(at)
	return args.Error(0)
}

type mockNotificationHub struct {
	mock.Mock
}

func (m *mockNotificationHub) Subscribe() (*inapp.Subscriber, func()) {
	args := m.Called()
	return args.Get(0).(*inapp.Subscriber), args.Get(1).(func())
}

func (m *mockNotificationHub) Publish(u inapp.Update) {
	m.Called(u)
}

// notificationServer builds a Server with mocked notification dependencies.
func notificationServer(t *testing.T, repo *mockNotificationRepository, hub NotificationHub) *Server {
	t.Helper()
	s, _, _, _ := setupServer(t)
	s.notifyRepo = repo
	s.notifyHub = hub
	return s
}

// --- humaMarkAllNotificationsRead ---

func TestHumaMarkAllNotificationsRead_Success(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	repo.On("MarkAllNotificationsRead", mock.AnythingOfType("time.Time")).Return(nil)
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUnreadCountChanged && u.UnreadCount == 0
	})).Return()

	s := notificationServer(t, repo, hub)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	repo.AssertExpectations(t)
	hub.AssertExpectations(t)
}

func TestHumaMarkAllNotificationsRead_RepoError(t *testing.T) {
	repo := new(mockNotificationRepository)

	repo.On("MarkAllNotificationsRead", mock.AnythingOfType("time.Time")).Return(errors.New("db error"))

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	repo.AssertExpectations(t)
}

// --- humaMarkNotificationRead ---

func TestHumaMarkNotificationRead_Success(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	n := &storage.Notification{ID: "01JT000000000000000000001", Kind: "run.failed"}
	repo.On("MarkNotificationRead", "01JT000000000000000000001", mock.AnythingOfType("time.Time")).Return(n, nil)
	repo.On("CountUnreadNotifications").Return(int64(3), nil)
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUpdated && u.Notification.ID == n.ID && u.UnreadCount == 3
	})).Return()

	s := notificationServer(t, repo, hub)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT000000000000000000001/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	repo.AssertExpectations(t)
	hub.AssertExpectations(t)
}

func TestHumaMarkNotificationRead_NotFound(t *testing.T) {
	repo := new(mockNotificationRepository)

	repo.On("MarkNotificationRead", "01JT000000000000000000001", mock.AnythingOfType("time.Time")).
		Return(nil, storage.ErrNotFound)

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT000000000000000000001/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	repo.AssertExpectations(t)
}

func TestHumaMarkNotificationRead_RepoError(t *testing.T) {
	repo := new(mockNotificationRepository)

	repo.On("MarkNotificationRead", "01JT000000000000000000001", mock.AnythingOfType("time.Time")).
		Return(nil, errors.New("db error"))

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT000000000000000000001/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	repo.AssertExpectations(t)
}

// --- humaMarkNotificationUnread ---

func TestHumaMarkNotificationUnread_Success(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	n := &storage.Notification{ID: "01JT000000000000000000002", Kind: "run.failed"}
	repo.On("MarkNotificationUnread", "01JT000000000000000000002").Return(n, nil)
	repo.On("CountUnreadNotifications").Return(int64(5), nil)
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUpdated && u.Notification.ID == n.ID && u.UnreadCount == 5
	})).Return()

	s := notificationServer(t, repo, hub)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT000000000000000000002/unread", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	repo.AssertExpectations(t)
	hub.AssertExpectations(t)
}

func TestHumaMarkNotificationUnread_NotFound(t *testing.T) {
	repo := new(mockNotificationRepository)

	repo.On("MarkNotificationUnread", "01JT000000000000000000002").Return(nil, storage.ErrNotFound)

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT000000000000000000002/unread", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	repo.AssertExpectations(t)
}

// --- publishNotificationUpdate ---

func TestPublishNotificationUpdate_NilHub_NoPanic(t *testing.T) {
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, nil)
	// Should not panic when hub is nil.
	s.publishNotificationUpdate(&storage.Notification{ID: "x"})
}

func TestPublishNotificationUpdate_NilNotification_NoPanic(t *testing.T) {
	hub := new(mockNotificationHub)
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, hub)
	// Should not panic when notification is nil.
	s.publishNotificationUpdate(nil)
	hub.AssertNotCalled(t, "Publish")
}

func TestPublishNotificationUpdate_CountQueryFails_ShipsNegativeCount(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	repo.On("CountUnreadNotifications").Return(int64(0), errors.New("db error"))
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.UnreadCount == -1
	})).Return()

	s := notificationServer(t, repo, hub)
	s.publishNotificationUpdate(&storage.Notification{ID: "y"})

	repo.AssertExpectations(t)
	hub.AssertExpectations(t)
}

// --- publishUnreadCountChanged ---

func TestPublishUnreadCountChanged_NilHub_NoPanic(t *testing.T) {
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, nil)
	// Should not panic.
	s.publishUnreadCountChanged(42)
}

func TestPublishUnreadCountChanged_PublishesCount(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUnreadCountChanged && u.UnreadCount == 7
	})).Return()

	s := notificationServer(t, repo, hub)
	s.publishUnreadCountChanged(7)

	hub.AssertExpectations(t)
}

// --- sseNotificationsPingLoop ---

func TestSseNotificationsPingLoop_ExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	done := make(chan struct{})
	go func() {
		defer close(done)
		sseNotificationsPingLoop(ctx, func(sse.Message) error { return nil })
	}()

	select {
	case <-done:
		// good — returned because context was cancelled
	case <-time.After(2 * time.Second):
		t.Fatal("sseNotificationsPingLoop did not exit after context cancellation")
	}
}

func TestSseNotificationsPingLoop_ExitsOnContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// 30s ticker won't fire in 50ms; context timeout terminates the loop.
		sseNotificationsPingLoop(ctx, func(sse.Message) error { return nil })
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sseNotificationsPingLoop did not exit")
	}
}

// --- SSE stream with no hub (ping-only) ---

func TestNotificationsStream_NoHub_SendsPing(t *testing.T) {
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, nil) // no hub → ping-only path

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "event: ping")
}

// --- List notifications ---

func TestListNotifications_Success(t *testing.T) {
	repo := new(mockNotificationRepository)
	now := time.Now()
	rows := []storage.Notification{
		{ID: "01JT000000000000000000001", Kind: "run.failed", CreatedAt: now, LastOccurredAt: now, Occurrences: []time.Time{now}},
	}
	repo.On("ListNotifications", 50, "").Return(rows, nil)

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body NotificationsListBody
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Len(t, body.Items, 1)
	assert.Equal(t, "01JT000000000000000000001", body.Items[0].ID)
	repo.AssertExpectations(t)
}

func TestListNotifications_RepoError(t *testing.T) {
	repo := new(mockNotificationRepository)
	repo.On("ListNotifications", 50, "").Return(nil, errors.New("db error"))

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	repo.AssertExpectations(t)
}
