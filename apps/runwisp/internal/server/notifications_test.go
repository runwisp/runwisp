// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func (m *mockNotificationRepository) UpsertByFingerprint(ctx context.Context, n *storage.Notification, window time.Duration, ringSize int) (bool, error) {
	args := m.Called(ctx, n, window, ringSize)
	return args.Bool(0), args.Error(1)
}

func (m *mockNotificationRepository) ListNotifications(ctx context.Context, limit int, before string) ([]storage.Notification, error) {
	args := m.Called(ctx, limit, before)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) GetNotificationByID(ctx context.Context, id string) (*storage.Notification, error) {
	args := m.MethodCalled("GetNotificationByID", ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) PruneNotificationsByCount(ctx context.Context, keep int) (int64, error) {
	args := m.MethodCalled("PruneNotificationsByCount", ctx, keep)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockNotificationRepository) PruneNotificationsByAge(ctx context.Context, olderThan time.Duration) (int64, error) {
	args := m.MethodCalled("PruneNotificationsByAge", ctx, olderThan)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockNotificationRepository) CountUnreadNotifications(ctx context.Context) (int64, error) {
	args := m.MethodCalled("CountUnreadNotifications", ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockNotificationRepository) MarkNotificationRead(ctx context.Context, id string, at time.Time) (*storage.Notification, error) {
	args := m.MethodCalled("MarkNotificationRead", ctx, id, at)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) MarkNotificationUnread(ctx context.Context, id string) (*storage.Notification, error) {
	args := m.MethodCalled("MarkNotificationUnread", ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Notification), args.Error(1)
}

func (m *mockNotificationRepository) MarkAllNotificationsRead(ctx context.Context, at time.Time) error {
	args := m.Called(ctx, at)
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

// --- humaUnreadNotificationCount ---

func TestHumaUnreadNotificationCount_Success(t *testing.T) {
	repo := new(mockNotificationRepository)
	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(17), nil)

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/unread-count", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Count int64 `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(17), body.Count)
	repo.AssertExpectations(t)
}

func TestHumaUnreadNotificationCount_RepoError(t *testing.T) {
	repo := new(mockNotificationRepository)
	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(0), errors.New("db down"))

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/unread-count", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- notifyUpdateToPayload ---

func TestNotifyUpdateToPayload_Created(t *testing.T) {
	n := storage.Notification{ID: "n1", Title: "hello"}
	got := notifyUpdateToPayload(inapp.Update{Type: inapp.UpdateTypeCreated, Notification: n, UnreadCount: 5})
	ev, ok := got.(NotificationCreatedEvent)
	require.True(t, ok)
	assert.Equal(t, int64(5), ev.UnreadCount)
	assert.Equal(t, "n1", ev.Notification.ID)
}

func TestNotifyUpdateToPayload_Updated(t *testing.T) {
	n := storage.Notification{ID: "n2"}
	got := notifyUpdateToPayload(inapp.Update{Type: inapp.UpdateTypeUpdated, Notification: n, UnreadCount: 1})
	ev, ok := got.(NotificationUpdatedEvent)
	require.True(t, ok)
	assert.Equal(t, "n2", ev.Notification.ID)
}

func TestNotifyUpdateToPayload_UnreadCountChanged(t *testing.T) {
	got := notifyUpdateToPayload(inapp.Update{Type: inapp.UpdateTypeUnreadCountChanged, UnreadCount: 99})
	ev, ok := got.(NotificationUnreadCountEvent)
	require.True(t, ok)
	assert.Equal(t, int64(99), ev.UnreadCount)
}

func TestNotifyUpdateToPayload_UnknownTypeFallsBackToUpdated(t *testing.T) {
	n := storage.Notification{ID: "fallback"}
	got := notifyUpdateToPayload(inapp.Update{Type: "made-up-type", Notification: n, UnreadCount: 2})
	ev, ok := got.(NotificationUpdatedEvent)
	require.True(t, ok)
	assert.Equal(t, "fallback", ev.Notification.ID)
}

// --- humaMarkAllNotificationsRead ---

func TestHumaMarkAllNotificationsRead_Success(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	repo.On("MarkAllNotificationsRead", mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)
	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(0), nil)
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

func TestHumaMarkAllNotificationsRead_ReQueriesRatherThanAssumingZero(t *testing.T) {
	// Regression: a notification created concurrently with the mark-all-read
	// UPDATE is still unread; the published count must reflect that instead
	// of hardcoding 0, or every other open tab's badge goes wrongly to zero.
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	repo.On("MarkAllNotificationsRead", mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)
	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(1), nil)
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUnreadCountChanged && u.UnreadCount == 1
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

func TestHumaMarkAllNotificationsRead_CountQueryFails_ShipsNegativeCount(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	repo.On("MarkAllNotificationsRead", mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)
	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(0), errors.New("db error"))
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUnreadCountChanged && u.UnreadCount == -1
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

	repo.On("MarkAllNotificationsRead", mock.Anything, mock.AnythingOfType("time.Time")).Return(errors.New("db error"))

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

	n := &storage.Notification{ID: "01JT0000000000000000000001", Kind: "run.failed"}
	repo.On("MarkNotificationRead", mock.Anything, "01JT0000000000000000000001", mock.AnythingOfType("time.Time")).Return(n, nil)
	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(3), nil)
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUpdated && u.Notification.ID == n.ID && u.UnreadCount == 3
	})).Return()

	s := notificationServer(t, repo, hub)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT0000000000000000000001/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	repo.AssertExpectations(t)
	hub.AssertExpectations(t)
}

func TestHumaMarkNotificationRead_NotFound(t *testing.T) {
	repo := new(mockNotificationRepository)

	repo.On("MarkNotificationRead", mock.Anything, "01JT0000000000000000000001", mock.AnythingOfType("time.Time")).
		Return(nil, storage.ErrNotFound)

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT0000000000000000000001/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	repo.AssertExpectations(t)
}

func TestHumaMarkNotificationRead_RepoError(t *testing.T) {
	repo := new(mockNotificationRepository)

	repo.On("MarkNotificationRead", mock.Anything, "01JT0000000000000000000001", mock.AnythingOfType("time.Time")).
		Return(nil, errors.New("db error"))

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT0000000000000000000001/read", nil)
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

	n := &storage.Notification{ID: "01JT0000000000000000000002", Kind: "run.failed"}
	repo.On("MarkNotificationUnread", mock.Anything, "01JT0000000000000000000002").Return(n, nil)
	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(5), nil)
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.Type == inapp.UpdateTypeUpdated && u.Notification.ID == n.ID && u.UnreadCount == 5
	})).Return()

	s := notificationServer(t, repo, hub)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT0000000000000000000002/unread", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	repo.AssertExpectations(t)
	hub.AssertExpectations(t)
}

func TestHumaMarkNotificationUnread_NotFound(t *testing.T) {
	repo := new(mockNotificationRepository)

	repo.On("MarkNotificationUnread", mock.Anything, "01JT0000000000000000000002").Return(nil, storage.ErrNotFound)

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/01JT0000000000000000000002/unread", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	repo.AssertExpectations(t)
}

// TestInvalidNotificationID guards notificationId path-param validation
// (added to match runId's existing minLength/maxLength/pattern — see
// TestInvalidRunID in server_test.go): a malformed ID must be rejected by
// huma before it ever reaches the repository, not forwarded as-is.
func TestInvalidNotificationID(t *testing.T) {
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/invalid-id/read", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	repo.AssertExpectations(t)
}

// --- publishNotificationUpdate ---

func TestPublishNotificationUpdate_NilHub_NoPanic(t *testing.T) {
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, nil)
	s.publishNotificationUpdate(context.Background(), &storage.Notification{ID: "x"})
}

func TestPublishNotificationUpdate_NilNotification_NoPanic(t *testing.T) {
	hub := new(mockNotificationHub)
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, hub)
	s.publishNotificationUpdate(context.Background(), nil)
	hub.AssertNotCalled(t, "Publish")
}

func TestPublishNotificationUpdate_CountQueryFails_ShipsNegativeCount(t *testing.T) {
	repo := new(mockNotificationRepository)
	hub := new(mockNotificationHub)

	repo.On("CountUnreadNotifications", mock.Anything).Return(int64(0), errors.New("db error"))
	hub.On("Publish", mock.MatchedBy(func(u inapp.Update) bool {
		return u.UnreadCount == -1
	})).Return()

	s := notificationServer(t, repo, hub)
	s.publishNotificationUpdate(context.Background(), &storage.Notification{ID: "y"})

	repo.AssertExpectations(t)
	hub.AssertExpectations(t)
}

// --- publishUnreadCountChanged ---

func TestPublishUnreadCountChanged_NilHub_NoPanic(t *testing.T) {
	repo := new(mockNotificationRepository)
	s := notificationServer(t, repo, nil)
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

// --- List notifications ---

func TestListNotifications_Success(t *testing.T) {
	repo := new(mockNotificationRepository)
	now := time.Now()
	rows := []storage.Notification{
		{ID: "01JT0000000000000000000001", Kind: "run.failed", CreatedAt: now, LastOccurredAt: now, Occurrences: []time.Time{now}},
	}
	repo.On("ListNotifications", mock.Anything, 50, "").Return(rows, nil)

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body NotificationsListBody
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Len(t, body.Items, 1)
	assert.Equal(t, "01JT0000000000000000000001", body.Items[0].ID)
	repo.AssertExpectations(t)
}

func TestListNotifications_RepoError(t *testing.T) {
	repo := new(mockNotificationRepository)
	repo.On("ListNotifications", mock.Anything, 50, "").Return(nil, errors.New("db error"))

	s := notificationServer(t, repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	repo.AssertExpectations(t)
}
