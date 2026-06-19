package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// workItemSession builds a session linked to a project whose classifier is keyed
// by the remote display name "acme/web". The model on the assistant message lets
// the pricing calculator produce a deterministic non-zero cost.
func workItemSession(id session.ID, sessionType string, inputTokens, outputTokens int, createdAt time.Time) *session.Session {
	return &session.Session{
		ID:          id,
		Provider:    "claude-code",
		RemoteURL:   "github.com/acme/web",
		ProjectPath: "/work/web",
		SessionType: sessionType,
		CreatedAt:   createdAt,
		TokenUsage: session.TokenUsage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
		},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do work"},
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", OutputTokens: outputTokens},
		},
	}
}

// newWorkItemConfig returns a config whose "acme/web" project carries the given
// ticket classifier rules.
func newWorkItemConfig(t *testing.T, pc config.ProjectClassifierConf) *config.Config {
	t.Helper()
	cfg, err := config.New(t.TempDir(), "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	if err := cfg.SetProjectClassifier("acme/web", pc); err != nil {
		t.Fatalf("SetProjectClassifier: %v", err)
	}
	return cfg
}

func TestWorkItems_aggregatesAndSorts(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// OMO-1: two sessions, small cost each.
	s1 := workItemSession("s1", "bug", 10_000, 5_000, base)
	s2 := workItemSession("s2", "bug", 10_000, 5_000, base.Add(time.Hour))
	// OMO-2: one session, much larger cost.
	s3 := workItemSession("s3", "feature", 200_000, 100_000, base.Add(2*time.Hour))

	store := testutil.NewMockStore(s1, s2, s3)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "OMO-1"}); err != nil {
		t.Fatalf("AddLink s1: %v", err)
	}
	if err := store.AddLink("s2", session.Link{LinkType: session.LinkTicket, Ref: "OMO-1"}); err != nil {
		t.Fatalf("AddLink s2: %v", err)
	}
	if err := store.AddLink("s3", session.Link{LinkType: session.LinkTicket, Ref: "OMO-2"}); err != nil {
		t.Fatalf("AddLink s3: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{
		TicketPattern: `OMO-\d+`,
		TicketSource:  "notion",
		TicketURL:     "https://notion.so/{id}",
	})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("Items length = %d, want 2", len(list.Items))
	}

	// Sorted by estimated cost descending: OMO-2 (bigger) first.
	if list.Items[0].Ref != "OMO-2" {
		t.Errorf("Items[0].Ref = %q, want OMO-2", list.Items[0].Ref)
	}
	if list.Items[1].Ref != "OMO-1" {
		t.Errorf("Items[1].Ref = %q, want OMO-1", list.Items[1].Ref)
	}
	if list.Items[0].EstimatedCost <= list.Items[1].EstimatedCost {
		t.Errorf("expected OMO-2 cost (%f) > OMO-1 cost (%f)",
			list.Items[0].EstimatedCost, list.Items[1].EstimatedCost)
	}

	omo1 := list.Items[1]
	if omo1.SessionCount != 2 {
		t.Errorf("OMO-1 SessionCount = %d, want 2", omo1.SessionCount)
	}
	if omo1.TotalTokens != 30_000 {
		t.Errorf("OMO-1 TotalTokens = %d, want 30000", omo1.TotalTokens)
	}
	if omo1.Source != "notion" {
		t.Errorf("OMO-1 Source = %q, want notion", omo1.Source)
	}
	if omo1.URL != "https://notion.so/OMO-1" {
		t.Errorf("OMO-1 URL = %q, want https://notion.so/OMO-1", omo1.URL)
	}
	if !omo1.FirstActivity.Equal(base) {
		t.Errorf("OMO-1 FirstActivity = %v, want %v", omo1.FirstActivity, base)
	}
	if !omo1.LastActivity.Equal(base.Add(time.Hour)) {
		t.Errorf("OMO-1 LastActivity = %v, want %v", omo1.LastActivity, base.Add(time.Hour))
	}

	// List totals reflect every item.
	wantSessions := 3
	if list.TotalSessions != wantSessions {
		t.Errorf("TotalSessions = %d, want %d", list.TotalSessions, wantSessions)
	}
	wantCost := list.Items[0].EstimatedCost + list.Items[1].EstimatedCost
	if list.TotalCost != wantCost {
		t.Errorf("TotalCost = %f, want %f", list.TotalCost, wantCost)
	}
}

func TestWorkItems_kindFromSessionType(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Two "bug" + one "feature" → dominant type is "bug".
	s1 := workItemSession("s1", "bug", 1_000, 500, base)
	s2 := workItemSession("s2", "bug", 1_000, 500, base.Add(time.Hour))
	s3 := workItemSession("s3", "feature", 1_000, 500, base.Add(2*time.Hour))

	store := testutil.NewMockStore(s1, s2, s3)
	for _, id := range []session.ID{"s1", "s2", "s3"} {
		if err := store.AddLink(id, session.Link{LinkType: session.LinkTicket, Ref: "OMO-9"}); err != nil {
			t.Fatalf("AddLink %s: %v", id, err)
		}
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{TicketPattern: `OMO-\d+`})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(list.Items))
	}
	if list.Items[0].Kind != "bug" {
		t.Errorf("Kind = %q, want bug", list.Items[0].Kind)
	}
}

func TestWorkItems_kindFromPrefix(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Session type is "feature", but prefix derivation should win for "BUG-12".
	s1 := workItemSession("s1", "feature", 1_000, 500, base)

	store := testutil.NewMockStore(s1)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "BUG-12"}); err != nil {
		t.Fatalf("AddLink: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{
		TicketPattern: `[A-Z]+-\d+`,
		KindFrom:      "prefix",
	})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(list.Items))
	}
	if list.Items[0].Kind != "bug" {
		t.Errorf("Kind = %q, want bug (from prefix)", list.Items[0].Kind)
	}
}

func TestWorkItems_filterByKind(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	bug := workItemSession("s1", "bug", 1_000, 500, base)
	feat := workItemSession("s2", "feature", 1_000, 500, base.Add(time.Hour))

	store := testutil.NewMockStore(bug, feat)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "OMO-1"}); err != nil {
		t.Fatalf("AddLink s1: %v", err)
	}
	if err := store.AddLink("s2", session.Link{LinkType: session.LinkTicket, Ref: "OMO-2"}); err != nil {
		t.Fatalf("AddLink s2: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{TicketPattern: `OMO-\d+`})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{Kind: "bug"})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(list.Items))
	}
	if list.Items[0].Ref != "OMO-1" || list.Items[0].Kind != "bug" {
		t.Errorf("got Ref=%q Kind=%q, want OMO-1/bug", list.Items[0].Ref, list.Items[0].Kind)
	}
}

func TestWorkItems_filterByProject(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	inProject := workItemSession("s1", "bug", 1_000, 500, base)
	other := workItemSession("s2", "bug", 1_000, 500, base.Add(time.Hour))
	other.ProjectPath = "/work/other"

	store := testutil.NewMockStore(inProject, other)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "OMO-1"}); err != nil {
		t.Fatalf("AddLink s1: %v", err)
	}
	if err := store.AddLink("s2", session.Link{LinkType: session.LinkTicket, Ref: "OMO-2"}); err != nil {
		t.Fatalf("AddLink s2: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{TicketPattern: `OMO-\d+`})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{ProjectPath: "/work/web"})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("Items length = %d, want 1 (only /work/web)", len(list.Items))
	}
	if list.Items[0].Ref != "OMO-1" {
		t.Errorf("Ref = %q, want OMO-1", list.Items[0].Ref)
	}
}

func TestWorkItems_empty(t *testing.T) {
	store := testutil.NewMockStore()
	svc := NewSessionService(SessionServiceConfig{Store: store})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("Items length = %d, want 0", len(list.Items))
	}
	if list.TotalCost != 0 || list.TotalSessions != 0 {
		t.Errorf("TotalCost=%f TotalSessions=%d, want 0/0", list.TotalCost, list.TotalSessions)
	}
}

func TestWorkItem_single(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s1 := workItemSession("s1", "bug", 10_000, 5_000, base)
	s2 := workItemSession("s2", "bug", 10_000, 5_000, base.Add(time.Hour))

	store := testutil.NewMockStore(s1, s2)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "OMO-904"}); err != nil {
		t.Fatalf("AddLink s1: %v", err)
	}
	if err := store.AddLink("s2", session.Link{LinkType: session.LinkTicket, Ref: "OMO-904"}); err != nil {
		t.Fatalf("AddLink s2: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{
		TicketPattern: `OMO-\d+`,
		TicketSource:  "notion",
		TicketURL:     "https://notion.so/{id}",
	})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	item, err := svc.WorkItem(context.Background(), "OMO-904")
	if err != nil {
		t.Fatalf("WorkItem: %v", err)
	}
	if item.Ref != "OMO-904" {
		t.Errorf("Ref = %q, want OMO-904", item.Ref)
	}
	if item.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", item.SessionCount)
	}
	if len(item.Sessions) != 2 {
		t.Errorf("Sessions length = %d, want 2 (detail view populates sessions)", len(item.Sessions))
	}
	if item.Source != "notion" || item.URL != "https://notion.so/OMO-904" {
		t.Errorf("Source/URL = %q/%q, want notion/https://notion.so/OMO-904", item.Source, item.URL)
	}
}

func TestWorkItem_notFound(t *testing.T) {
	store := testutil.NewMockStore()
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.WorkItem(context.Background(), "NOPE-1")
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
}

func TestWorkItems_forkAwareDedup(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s1 := workItemSession("s1", "feature", 10_000, 10_000, base)
	s2 := workItemSession("s2", "feature", 10_000, 10_000, base.Add(time.Hour))

	store := testutil.NewMockStore(s1, s2)
	for _, id := range []session.ID{"s1", "s2"} {
		if err := store.AddLink(id, session.Link{LinkType: session.LinkTicket, Ref: "OMO-1"}); err != nil {
			t.Fatalf("AddLink %s: %v", id, err)
		}
	}

	// s2 is a fork of s1 sharing 15k of the 20k tokens as prefix.
	if err := store.SaveForkRelation(session.ForkRelation{
		OriginalID:         "s1",
		ForkID:             "s2",
		SharedInputTokens:  7_500,
		SharedOutputTokens: 7_500,
	}); err != nil {
		t.Fatalf("SaveForkRelation: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{TicketPattern: `OMO-\d+`})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(list.Items))
	}

	item := list.Items[0]
	// s1: 20k tokens (original, no deduction).
	// s2: 20k - 15k shared = 5k net tokens.
	// total = 25k, not 40k.
	if item.TotalTokens != 25_000 {
		t.Errorf("TotalTokens = %d, want 25000 (fork dedup reduces shared prefix)", item.TotalTokens)
	}

	// Compare against a naive (no fork relation) service to verify cost is lower.
	storeCopy := testutil.NewMockStore(s1, s2)
	for _, id := range []session.ID{"s1", "s2"} {
		_ = storeCopy.AddLink(id, session.Link{LinkType: session.LinkTicket, Ref: "OMO-1"})
	}
	svcNaive := NewSessionService(SessionServiceConfig{Store: storeCopy, Config: cfg})
	naiveList, _ := svcNaive.WorkItems(context.Background(), WorkItemRequest{})
	if len(naiveList.Items) != 1 {
		t.Fatalf("naive list should have 1 item")
	}
	if item.EstimatedCost >= naiveList.Items[0].EstimatedCost {
		t.Errorf("fork dedup: cost %.6f should be < naive %.6f", item.EstimatedCost, naiveList.Items[0].EstimatedCost)
	}
}

func TestWorkItems_filterBySource(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s1 := workItemSession("s1", "feature", 1_000, 500, base)
	s2 := workItemSession("s2", "bug", 1_000, 500, base.Add(time.Hour))

	store := testutil.NewMockStore(s1, s2)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "OMO-10"}); err != nil {
		t.Fatalf("AddLink s1: %v", err)
	}
	if err := store.AddLink("s2", session.Link{LinkType: session.LinkTicket, Ref: "JIRA-42"}); err != nil {
		t.Fatalf("AddLink s2: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{
		TicketSources: []config.TicketSourceConf{
			{Name: "notion", TicketPattern: `OMO-\d+`, TicketURL: "https://notion.so/{id}"},
			{Name: "jira", TicketPattern: `JIRA-\d+`, TicketURL: "https://jira.example.com/browse/{id}"},
		},
	})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	notionList, err := svc.WorkItems(context.Background(), WorkItemRequest{Source: "notion"})
	if err != nil {
		t.Fatalf("WorkItems notion: %v", err)
	}
	if len(notionList.Items) != 1 {
		t.Fatalf("notion filter: Items length = %d, want 1", len(notionList.Items))
	}
	if notionList.Items[0].Ref != "OMO-10" || notionList.Items[0].Source != "notion" {
		t.Errorf("notion filter: got Ref=%q Source=%q, want OMO-10/notion", notionList.Items[0].Ref, notionList.Items[0].Source)
	}
	if notionList.Items[0].URL != "https://notion.so/OMO-10" {
		t.Errorf("notion URL = %q, want https://notion.so/OMO-10", notionList.Items[0].URL)
	}

	jiraList, err := svc.WorkItems(context.Background(), WorkItemRequest{Source: "jira"})
	if err != nil {
		t.Fatalf("WorkItems jira: %v", err)
	}
	if len(jiraList.Items) != 1 {
		t.Fatalf("jira filter: Items length = %d, want 1", len(jiraList.Items))
	}
	if jiraList.Items[0].Ref != "JIRA-42" || jiraList.Items[0].Source != "jira" {
		t.Errorf("jira filter: got Ref=%q Source=%q, want JIRA-42/jira", jiraList.Items[0].Ref, jiraList.Items[0].Source)
	}
}

func TestWorkItems_multiSourceURLDerivation(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s1 := workItemSession("s1", "feature", 1_000, 500, base)
	s2 := workItemSession("s2", "bug", 1_000, 500, base.Add(time.Hour))

	store := testutil.NewMockStore(s1, s2)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "OMO-5"}); err != nil {
		t.Fatalf("AddLink s1: %v", err)
	}
	if err := store.AddLink("s2", session.Link{LinkType: session.LinkTicket, Ref: "JIRA-9"}); err != nil {
		t.Fatalf("AddLink s2: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{
		TicketSources: []config.TicketSourceConf{
			{Name: "notion", TicketPattern: `OMO-\d+`, TicketURL: "https://notion.so/{id}"},
			{Name: "jira", TicketPattern: `JIRA-\d+`, TicketURL: "https://jira.example.com/{id}"},
		},
	})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("Items length = %d, want 2", len(list.Items))
	}

	byRef := map[string]session.WorkItem{}
	for _, it := range list.Items {
		byRef[it.Ref] = it
	}

	omo := byRef["OMO-5"]
	if omo.Source != "notion" {
		t.Errorf("OMO-5 Source = %q, want notion", omo.Source)
	}
	if omo.URL != "https://notion.so/OMO-5" {
		t.Errorf("OMO-5 URL = %q, want https://notion.so/OMO-5", omo.URL)
	}

	jira := byRef["JIRA-9"]
	if jira.Source != "jira" {
		t.Errorf("JIRA-9 Source = %q, want jira", jira.Source)
	}
	if jira.URL != "https://jira.example.com/JIRA-9" {
		t.Errorf("JIRA-9 URL = %q, want https://jira.example.com/JIRA-9", jira.URL)
	}
}

func TestWorkItems_sourceFilterExcludesUnknown(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s1 := workItemSession("s1", "feature", 1_000, 500, base)

	store := testutil.NewMockStore(s1)
	if err := store.AddLink("s1", session.Link{LinkType: session.LinkTicket, Ref: "OMO-7"}); err != nil {
		t.Fatalf("AddLink: %v", err)
	}

	cfg := newWorkItemConfig(t, config.ProjectClassifierConf{
		TicketSources: []config.TicketSourceConf{
			{Name: "notion", TicketPattern: `OMO-\d+`, TicketURL: "https://notion.so/{id}"},
		},
	})
	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	list, err := svc.WorkItems(context.Background(), WorkItemRequest{Source: "linear"})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("Items length = %d, want 0 (linear source has no matches)", len(list.Items))
	}
}

func TestWorkItems_sameRefDifferentSourcesStaySeparate(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	notion := workItemSession("s1", "feature", 1_000, 500, base)
	jira := workItemSession("s2", "bug", 2_000, 500, base.Add(time.Hour))
	jira.RemoteURL = "github.com/acme/backend"
	jira.ProjectPath = "/work/backend"

	store := testutil.NewMockStore(notion, jira)
	for _, id := range []session.ID{"s1", "s2"} {
		if err := store.AddLink(id, session.Link{LinkType: session.LinkTicket, Ref: "TASK-1"}); err != nil {
			t.Fatalf("AddLink %s: %v", id, err)
		}
	}

	cfg, err := config.New(t.TempDir(), "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	if err := cfg.SetProjectClassifier("acme/web", config.ProjectClassifierConf{
		TicketSources: []config.TicketSourceConf{{Name: "notion", TicketPattern: `TASK-\d+`, TicketURL: "https://notion.so/{id}"}},
	}); err != nil {
		t.Fatalf("SetProjectClassifier notion: %v", err)
	}
	if err := cfg.SetProjectClassifier("acme/backend", config.ProjectClassifierConf{
		TicketSources: []config.TicketSourceConf{{Name: "jira", TicketPattern: `TASK-\d+`, TicketURL: "https://jira.example.com/{id}"}},
	}); err != nil {
		t.Fatalf("SetProjectClassifier jira: %v", err)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})
	list, err := svc.WorkItems(context.Background(), WorkItemRequest{})
	if err != nil {
		t.Fatalf("WorkItems: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("Items length = %d, want 2 separate source/ref entries", len(list.Items))
	}
	seen := map[string]bool{}
	for _, it := range list.Items {
		seen[it.Source+":"+it.Ref] = true
	}
	if !seen["notion:TASK-1"] || !seen["jira:TASK-1"] {
		t.Fatalf("sources = %#v, want notion:TASK-1 and jira:TASK-1", seen)
	}

	jiraOnly, err := svc.WorkItem(context.Background(), "TASK-1", WorkItemRequest{Source: "jira"})
	if err != nil {
		t.Fatalf("WorkItem jira: %v", err)
	}
	if jiraOnly.Source != "jira" || jiraOnly.SessionCount != 1 || len(jiraOnly.Sessions) != 1 || jiraOnly.Sessions[0].ID != "s2" {
		t.Fatalf("jira detail = source %q count %d sessions %#v, want only s2", jiraOnly.Source, jiraOnly.SessionCount, jiraOnly.Sessions)
	}
}

func TestSetProjectClassifier_invalidTicketSourcesPattern(t *testing.T) {
	cfg, err := config.New(t.TempDir(), "")
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	err = cfg.SetProjectClassifier("proj", config.ProjectClassifierConf{
		TicketSources: []config.TicketSourceConf{
			{Name: "notion", TicketPattern: `[invalid`},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid ticket_sources pattern")
	}
	if !strings.Contains(err.Error(), "ticket_sources") {
		t.Errorf("error should mention ticket_sources, got: %v", err)
	}
}
