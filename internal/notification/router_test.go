package notification

import "testing"

func TestDefaultRouter_BudgetAlert(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "#general",
		Alerts:         AlertConfig{Budget: true},
	})

	recipients := router.Route(Event{Type: EventBudgetAlert, Project: "org/repo"})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
	if recipients[0].Target != "#general" {
		t.Errorf("target = %q, want #general", recipients[0].Target)
	}
}

func TestDefaultRouter_BudgetAlert_Disabled(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "#general",
		Alerts:         AlertConfig{Budget: false},
	})

	recipients := router.Route(Event{Type: EventBudgetAlert})
	if len(recipients) != 0 {
		t.Errorf("expected 0 recipients when budget alerts disabled, got %d", len(recipients))
	}
}

func TestDefaultRouter_ProjectChannelOverride(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel:  "#general",
		ProjectChannels: map[string]string{"org/backend": "#backend-ai"},
		Alerts:          AlertConfig{Budget: true},
	})

	recipients := router.Route(Event{Type: EventBudgetAlert, Project: "org/backend"})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
	if recipients[0].Target != "#backend-ai" {
		t.Errorf("target = %q, want #backend-ai", recipients[0].Target)
	}
}

func TestDefaultRouter_ProjectFallsBackToDefault(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel:  "#general",
		ProjectChannels: map[string]string{"org/backend": "#backend-ai"},
		Alerts:          AlertConfig{Budget: true},
	})

	// Different project — should fall back to default
	recipients := router.Route(Event{Type: EventBudgetAlert, Project: "org/frontend"})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
	if recipients[0].Target != "#general" {
		t.Errorf("target = %q, want #general (fallback)", recipients[0].Target)
	}
}

func TestDefaultRouter_ErrorSpike(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "#alerts",
		Alerts:         AlertConfig{Errors: true},
	})

	recipients := router.Route(Event{Type: EventErrorSpike})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
}

func TestDefaultRouter_SessionCaptured_Disabled(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "#general",
		Alerts:         AlertConfig{Capture: false},
	})

	recipients := router.Route(Event{Type: EventSessionCaptured})
	if len(recipients) != 0 {
		t.Errorf("expected 0 recipients when capture alerts disabled, got %d", len(recipients))
	}
}

func TestDefaultRouter_DailyDigest(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "#daily",
		Digest:         DigestConfig{Daily: true},
	})

	recipients := router.Route(Event{Type: EventDailyDigest})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
}

func TestDefaultRouter_WeeklyReport_Disabled(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "#weekly",
		Digest:         DigestConfig{Weekly: false},
	})

	recipients := router.Route(Event{Type: EventWeeklyReport})
	if len(recipients) != 0 {
		t.Errorf("expected 0 recipients when weekly disabled, got %d", len(recipients))
	}
}

func TestDefaultRouter_PersonalDigest_DM(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		Digest: DigestConfig{Personal: true},
	})

	recipients := router.Route(Event{Type: EventPersonalDaily, OwnerID: "U0123ABC"})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
	if recipients[0].Type != RecipientDM {
		t.Errorf("type = %q, want dm", recipients[0].Type)
	}
	if recipients[0].Target != "U0123ABC" {
		t.Errorf("target = %q, want U0123ABC", recipients[0].Target)
	}
}

func TestDefaultRouter_PersonalDigest_NoOwner(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		Digest: DigestConfig{Personal: true},
	})

	recipients := router.Route(Event{Type: EventPersonalDaily, OwnerID: ""})
	if len(recipients) != 0 {
		t.Errorf("expected 0 recipients when no owner, got %d", len(recipients))
	}
}

func TestDefaultRouter_NoDefaultChannel(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "",
		Alerts:         AlertConfig{Budget: true},
	})

	recipients := router.Route(Event{Type: EventBudgetAlert})
	if len(recipients) != 0 {
		t.Errorf("expected 0 recipients when no default channel, got %d", len(recipients))
	}
}

func TestDefaultRouter_Recommendation_RoutesToProject(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel:  "#general",
		ProjectChannels: map[string]string{"org/backend": "#backend-ai"},
	})

	recipients := router.Route(Event{Type: EventRecommendation, Project: "org/backend"})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
	if recipients[0].Target != "#backend-ai" {
		t.Errorf("target = %q, want #backend-ai", recipients[0].Target)
	}
}

func TestDefaultRouter_Recommendation_FallsBackToDefault(t *testing.T) {
	router := NewDefaultRouter(RoutingConfig{
		DefaultChannel: "#general",
	})

	recipients := router.Route(Event{Type: EventRecommendation, Project: "org/unknown"})
	if len(recipients) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(recipients))
	}
	if recipients[0].Target != "#general" {
		t.Errorf("target = %q, want #general", recipients[0].Target)
	}
}

func TestDefaultRouter_NilRouter(t *testing.T) {
	var router *DefaultRouter
	recipients := router.Route(Event{Type: EventBudgetAlert})
	if recipients != nil {
		t.Errorf("expected nil from nil router, got %v", recipients)
	}
}
