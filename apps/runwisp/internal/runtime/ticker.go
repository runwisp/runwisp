// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"time"

	"log/slog"
)

// startTicker launches a goroutine that calls onTick every interval until ctx
// is cancelled, logging stopMsg at debug level on exit. It owns the ticker and
// the goroutine; the caller owns ctx's cancellation (and therefore Stop()).
func startTicker(ctx context.Context, interval time.Duration, stopMsg string, onTick func(context.Context)) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				onTick(ctx)
			case <-ctx.Done():
				slog.Debug(stopMsg)
				return
			}
		}
	}()
}
