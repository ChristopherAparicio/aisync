package notification

// ── Port Interfaces ──
//
// These interfaces define the contracts between the notification domain
// and its infrastructure adapters. Each adapter (Slack, webhook, email)
// implements Channel + Formatter. The Router is a domain service.

// Channel is the outbound port for delivering notifications.
// Each adapter (Slack, webhook, email) implements this interface.
type Channel interface {
	// Name returns the channel adapter identifier (e.g. "slack", "webhook", "email").
	Name() string

	// Send delivers a rendered message to a recipient.
	// The recipient type (channel vs DM) and target are adapter-specific.
	// Returns nil on success. Implementations should handle retries internally.
	Send(recipient Recipient, message RenderedMessage) error
}

// Formatter renders a notification Event into a RenderedMessage
// suitable for a specific channel adapter.
// Each adapter provides its own Formatter (Block Kit for Slack, JSON for webhooks, etc.).
type Formatter interface {
	// Format renders an event into a channel-specific message.
	// Returns a RenderedMessage ready for Channel.Send().
	Format(event Event) (RenderedMessage, error)
}

// Router determines who should receive a notification event.
// The routing logic considers:
//   - Event type (alerts → channel, personal → DM)
//   - Project config (channel overrides)
//   - Owner identity (human → DM, machine → admin DM, unknown → channel only)
type Router interface {
	// Route returns the list of recipients for a given event.
	// Returns an empty slice if no recipients are configured for this event.
	Route(event Event) []Recipient
}
