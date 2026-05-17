// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import "github.com/oklog/ulid/v2"

// reportDeliveryFailure synthesizes a notify.Event of kind delivery_failed and
// routes it directly into the failure sink (in-app coalescer), bypassing the
// router. The original event's Kind is preserved in Extra so the UI can show
// "delivery to slack-ops failed for run.failed".
func reportDeliveryFailure(sink SyntheticIngester, clock Clocker, actionID string, original *Event, cause error) {
	syn := &Event{
		ID:        ulid.Make().String(),
		Kind:      KindNotifyDeliveryFailed,
		Severity:  SevWarn,
		Timestamp: clock.Now(),
		Reason:    cause.Error(),
		Extra: map[string]any{
			"channel":       actionID,
			"original_kind": string(original.Kind),
			"task_name":     original.TaskName,
		},
	}
	if original != nil {
		syn.TaskName = original.TaskName
	}
	sink.IngestSynthetic(syn)
}
