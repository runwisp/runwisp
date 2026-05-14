// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func TestPersistenceCoordinatorPersistNew(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	defer pc.Shutdown()

	var called bool
	var isNewArg bool
	pc.hook = func(run *model.Run, isNew bool) {
		called = true
		isNewArg = isNew
	}

	pc.PersistNew(&model.Run{TaskName: "test"})
	time.Sleep(50 * time.Millisecond)

	assert.True(t, called)
	assert.True(t, isNewArg)
}

func TestPersistenceCoordinatorPersistExisting(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	defer pc.Shutdown()

	var isNewArg bool
	pc.hook = func(run *model.Run, isNew bool) {
		isNewArg = isNew
	}

	pc.PersistExisting(&model.Run{TaskName: "test"})
	time.Sleep(50 * time.Millisecond)

	assert.False(t, isNewArg)
}

func TestPersistenceCoordinatorNoHookNoPanic(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	defer pc.Shutdown()

	assert.NotPanics(t, func() {
		pc.PersistNew(&model.Run{})
		pc.PersistExisting(&model.Run{})
	})
}

func TestPersistenceCoordinatorShutdownDrains(t *testing.T) {
	pc := NewPersistenceCoordinator(100)

	var count int32
	pc.hook = func(run *model.Run, isNew bool) {
		atomic.AddInt32(&count, 1)
	}

	for i := 0; i < 10; i++ {
		pc.PersistNew(&model.Run{})
	}

	pc.Shutdown()
	assert.Equal(t, int32(10), atomic.LoadInt32(&count))
}

func TestPersistenceCoordinatorAfterShutdown(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	pc.hook = func(run *model.Run, isNew bool) {}
	pc.Shutdown()

	assert.NotPanics(t, func() {
		pc.PersistNew(&model.Run{})
		pc.PersistExisting(&model.Run{})
	})
}

func TestPublishRunNilBus(t *testing.T) {
	// Nil bus should not panic
	jm := NewTaskManager(new(testutil.MockExecutor), nil, time.Now).(*defaultTaskManager)
	assert.NotPanics(t, func() {
		jm.publishRun("test", &model.Run{})
	})
}
