// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

type LifecycleState string

const (
	StateBoot             LifecycleState = "boot"
	StateConnecting       LifecycleState = "connecting"
	StateAuthenticated    LifecycleState = "authenticated"
	StateSyncing          LifecycleState = "syncing"
	StateReady            LifecycleState = "ready"
	StateExecuting        LifecycleState = "executing"
	StateConnectingFailed LifecycleState = "connecting_failed"
	StateReconnecting     LifecycleState = "reconnecting"
)
