package telemetry

// NoopCollector implements Collector but discards all events.
// Used when telemetry is disabled (the default).
type NoopCollector struct{}

// Track discards the event.
func (NoopCollector) Track(Event) {}

// Flush is a no-op.
func (NoopCollector) Flush() error { return nil }

// Enabled always returns false.
func (NoopCollector) Enabled() bool { return false }
