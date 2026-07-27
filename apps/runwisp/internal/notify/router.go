// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

// Rule binds a predicate to a list of channel IDs to invoke when the predicate
// matches. ChannelIDs are resolved against the channel map at routing time;
// unknown IDs are skipped (validation happens at config load).
type Rule struct {
	Match     Predicate
	ActionIDs []string
}

// Router applies a flat list of rules in declaration order. Each event may
// match multiple rules; resulting channels are deduplicated.
type Router struct {
	rules    []Rule
	channels map[string]Channel
}

// NewRouter wires a rule list to the channel map.
func NewRouter(rules []Rule, channels map[string]Channel) *Router {
	return &Router{rules: rules, channels: channels}
}

// Route returns the (deduplicated) Channels whose rule predicates match ev.
// The synthetic KindNotifyDeliveryFailed event is delivered directly to the
// in-app channel by the dispatcher and never reaches Route — that's the cycle
// guard.
func (r *Router) Route(ev *Event) []Channel {
	if r == nil || ev == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(r.rules))
	out := make([]Channel, 0, len(r.rules))
	for _, rule := range r.rules {
		if rule.Match == nil || !rule.Match(ev) {
			continue
		}
		for _, id := range rule.ActionIDs {
			if _, dup := seen[id]; dup {
				continue
			}
			c, ok := r.channels[id]
			if !ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, c)
		}
	}
	return out
}
