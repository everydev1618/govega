// Package events is the domain-event spine: a single typed, causation-aware
// bus that any part of Vega can publish onto and subscribe to. The SSE broker
// and worker telemetry become projections of it; the reactive trigger router
// is a third subscriber. See docs/reactive-agents-design.md (decision D3).
package events

import "time"

// Origin carries typed causation — what caused this event. It is nil for
// external/root events (a human message, a cron fire, a sensor). When a
// reactive run emits events, they inherit the triggering event's Origin with
// Depth incremented, which is what the loop guard uses to cap reactive chains
// (design D2 / §5.3).
type Origin struct {
	EventID   string // the event that triggered the run that emitted this one
	AgentName string // the agent whose reactive run emitted it
	Depth     int    // reactive-chain depth
}

// Event is a single occurrence on the spine. Type is a dotted noun.verb string
// (e.g. "agent.completed"). Data carries the event-specific payload. ID and
// Time are stamped by the bus at Publish if left zero.
type Event struct {
	ID     string
	Type   string
	Data   map[string]any
	Origin *Origin
	Time   time.Time
}
