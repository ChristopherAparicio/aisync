package notification

// ── Router Configuration ──

// RoutingConfig holds the routing rules for notifications.
type RoutingConfig struct {
	// DefaultChannel is the fallback channel for all notifications
	// when no project-specific override is configured (e.g. "#ai-sessions").
	DefaultChannel string

	// ProjectChannels maps project names to their dedicated channels.
	// E.g. "org/backend" → "#backend-ai"
	ProjectChannels map[string]string

	// AlertTypes controls which alert types are enabled.
	Alerts AlertConfig

	// DigestConfig controls digest scheduling preferences.
	Digest DigestConfig
}

// AlertConfig toggles individual alert types.
type AlertConfig struct {
	Budget          bool // budget threshold alerts
	Errors          bool // error spike alerts
	Capture         bool // session captured notifications (usually noisy)
	ErrorThreshold  int  // number of errors to trigger a spike alert
	ErrorWindowMins int  // time window in minutes for error spike detection
}

// DigestConfig controls digest preferences.
type DigestConfig struct {
	Daily    bool // enable daily digests
	Weekly   bool // enable weekly reports
	Personal bool // enable personal DMs (requires bot mode)
}

// ── Default Router ──

// DefaultRouter routes events to recipients based on configuration.
// It supports:
//   - Project-specific channel overrides
//   - Default channel fallback
//   - Alert type filtering (budget, errors, capture)
//   - Digest enable/disable
type DefaultRouter struct {
	cfg RoutingConfig
}

// NewDefaultRouter creates a router with the given configuration.
func NewDefaultRouter(cfg RoutingConfig) *DefaultRouter {
	return &DefaultRouter{cfg: cfg}
}

// Route returns the list of recipients for an event.
func (r *DefaultRouter) Route(event Event) []Recipient {
	if r == nil {
		return nil
	}

	switch event.Type {
	case EventBudgetAlert:
		if !r.cfg.Alerts.Budget {
			return nil
		}
		return r.projectRecipients(event)

	case EventErrorSpike:
		if !r.cfg.Alerts.Errors {
			return nil
		}
		return r.projectRecipients(event)

	case EventSessionCaptured:
		if !r.cfg.Alerts.Capture {
			return nil
		}
		return r.projectRecipients(event)

	case EventDailyDigest:
		if !r.cfg.Digest.Daily {
			return nil
		}
		return r.projectRecipients(event)

	case EventWeeklyReport:
		if !r.cfg.Digest.Weekly {
			return nil
		}
		return r.projectRecipients(event)

	case EventPersonalDaily:
		if !r.cfg.Digest.Personal {
			return nil
		}
		// Personal events go to the owner's DM
		if event.OwnerID != "" {
			return []Recipient{{
				Type:   RecipientDM,
				Target: event.OwnerID,
				Name:   "personal digest",
			}}
		}
		return nil

	case EventRecommendation:
		// Recommendations always go to the project channel (or default).
		return r.projectRecipients(event)

	default:
		// Unknown event type — send to default channel if configured
		return r.defaultRecipient()
	}
}

// projectRecipients returns the channel for the event's project,
// falling back to the default channel.
func (r *DefaultRouter) projectRecipients(event Event) []Recipient {
	// Try project-specific channel
	if event.Project != "" && len(r.cfg.ProjectChannels) > 0 {
		if ch, ok := r.cfg.ProjectChannels[event.Project]; ok && ch != "" {
			return []Recipient{{
				Type:   RecipientChannel,
				Target: ch,
				Name:   event.Project,
			}}
		}
	}

	// Fall back to default
	return r.defaultRecipient()
}

// defaultRecipient returns a single-element slice with the default channel,
// or nil if no default is configured.
func (r *DefaultRouter) defaultRecipient() []Recipient {
	if r.cfg.DefaultChannel == "" {
		return nil
	}
	return []Recipient{{
		Type:   RecipientChannel,
		Target: r.cfg.DefaultChannel,
		Name:   "default",
	}}
}
