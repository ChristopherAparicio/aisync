// Package telemetry provides opt-in anonymous usage statistics collection.
//
// This is a foundation layer only. Events are tracked locally via a Collector
// interface. A future remote endpoint can be added by implementing a new
// Collector that sends events over the network.
//
// Telemetry is disabled by default and must be explicitly enabled via
// the "telemetry.enabled" configuration key.
package telemetry

import "time"

// Pre-defined event names for consistent tracking across the codebase.
const (
	EventSessionCaptured = "session.captured"
	EventSessionAnalyzed = "session.analyzed"
	EventCommandExecuted = "command.executed"
	EventServerStarted   = "server.started"
)

// Event represents a single telemetry data point.
type Event struct {
	Name       string            `json:"name"`
	Timestamp  time.Time         `json:"timestamp"`
	Properties map[string]string `json:"properties,omitempty"`
}

// NewEvent creates an Event with the given name and current timestamp.
func NewEvent(name string, properties map[string]string) Event {
	return Event{
		Name:       name,
		Timestamp:  time.Now(),
		Properties: properties,
	}
}

// Collector defines the interface for telemetry event collection.
// Implementations decide how events are stored or transmitted.
type Collector interface {
	// Track records a single telemetry event.
	Track(event Event)

	// Flush ensures all buffered events are persisted.
	// For collectors that write immediately, this may be a no-op.
	Flush() error

	// Enabled reports whether this collector is actively collecting events.
	Enabled() bool
}
