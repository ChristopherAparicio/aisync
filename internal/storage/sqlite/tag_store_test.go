package sqlite

import (
	"reflect"
	"sort"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── NormalizeTag ──

func TestNormalizeTag(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"Bug", "bug"},
		{"  Bug  ", "bug"},
		{"In Progress", "in-progress"},
		{"in   progress", "in-progress"},
		{"Multi  Word   Tag", "multi-word-tag"},
		{"UPPER", "upper"},
	}
	for _, c := range cases {
		got := NormalizeTag(c.in)
		if got != c.want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── AddTags ──

func TestAddTags_InsertAndDedup(t *testing.T) {
	store := mustOpenStore(t)
	s := testSession("ses_tag_add_1")
	if err := store.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mixed casing + duplicates + empty + whitespace.
	n, err := store.AddTags(s.ID, []string{"Bug", "bug", "  ", "In Progress", "in-progress", ""})
	if err != nil {
		t.Fatalf("AddTags: %v", err)
	}
	if n != 2 {
		t.Errorf("AddTags inserted = %d, want 2 (bug, in-progress)", n)
	}

	got, err := store.GetTags(s.ID)
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	want := []string{"bug", "in-progress"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetTags = %v, want %v", got, want)
	}
}

func TestAddTags_IdempotentSecondCall(t *testing.T) {
	store := mustOpenStore(t)
	s := testSession("ses_tag_add_2")
	if err := store.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := store.AddTags(s.ID, []string{"alpha", "beta"}); err != nil {
		t.Fatalf("AddTags first: %v", err)
	}
	n, err := store.AddTags(s.ID, []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatalf("AddTags second: %v", err)
	}
	if n != 1 {
		t.Errorf("second AddTags inserted = %d, want 1 (only gamma)", n)
	}
	tags, _ := store.GetTags(s.ID)
	if !reflect.DeepEqual(tags, []string{"alpha", "beta", "gamma"}) {
		t.Errorf("GetTags = %v, want [alpha beta gamma]", tags)
	}
}

func TestAddTags_EmptyAndNoOps(t *testing.T) {
	store := mustOpenStore(t)
	s := testSession("ses_tag_add_empty")
	if err := store.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	n, err := store.AddTags(s.ID, nil)
	if err != nil {
		t.Fatalf("AddTags(nil): %v", err)
	}
	if n != 0 {
		t.Errorf("AddTags(nil) = %d, want 0", n)
	}

	n, err = store.AddTags(s.ID, []string{"", "  "})
	if err != nil {
		t.Fatalf("AddTags(blank): %v", err)
	}
	if n != 0 {
		t.Errorf("AddTags(blank) = %d, want 0", n)
	}

	if _, err := store.AddTags("", []string{"a"}); err == nil {
		t.Errorf("AddTags(empty id) should error")
	}
}

// ── RemoveTags ──

func TestRemoveTags(t *testing.T) {
	store := mustOpenStore(t)
	s := testSession("ses_tag_rm")
	if err := store.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.AddTags(s.ID, []string{"alpha", "beta", "gamma"}); err != nil {
		t.Fatalf("AddTags: %v", err)
	}

	// Remove with mixed casing + a non-existent tag.
	n, err := store.RemoveTags(s.ID, []string{"ALPHA", "ghost"})
	if err != nil {
		t.Fatalf("RemoveTags: %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveTags = %d, want 1", n)
	}

	tags, _ := store.GetTags(s.ID)
	if !reflect.DeepEqual(tags, []string{"beta", "gamma"}) {
		t.Errorf("GetTags after remove = %v, want [beta gamma]", tags)
	}

	// Remove all remaining.
	n, err = store.RemoveTags(s.ID, []string{"beta", "gamma"})
	if err != nil {
		t.Fatalf("RemoveTags all: %v", err)
	}
	if n != 2 {
		t.Errorf("RemoveTags all = %d, want 2", n)
	}

	tags, _ = store.GetTags(s.ID)
	if len(tags) != 0 {
		t.Errorf("GetTags after full remove = %v, want empty", tags)
	}
}

// ── FK CASCADE on session delete ──

func TestSessionTags_CascadeOnDelete(t *testing.T) {
	store := mustOpenStore(t)
	s := testSession("ses_tag_cascade")
	if err := store.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.AddTags(s.ID, []string{"foo", "bar"}); err != nil {
		t.Fatalf("AddTags: %v", err)
	}
	if err := store.Delete(s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	tags, err := store.GetTags(s.ID)
	if err != nil {
		t.Fatalf("GetTags after delete: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("tags remained after session delete: %v", tags)
	}
}

// ── GetTagsBatch ──

func TestGetTagsBatch(t *testing.T) {
	store := mustOpenStore(t)
	s1 := testSession("ses_batch_1")
	s2 := testSession("ses_batch_2")
	s3 := testSession("ses_batch_3") // no tags
	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save %s: %v", s.ID, err)
		}
	}
	if _, err := store.AddTags(s1.ID, []string{"red", "blue"}); err != nil {
		t.Fatalf("AddTags s1: %v", err)
	}
	if _, err := store.AddTags(s2.ID, []string{"green"}); err != nil {
		t.Fatalf("AddTags s2: %v", err)
	}

	got, err := store.GetTagsBatch([]session.ID{s1.ID, s2.ID, s3.ID})
	if err != nil {
		t.Fatalf("GetTagsBatch: %v", err)
	}

	if !reflect.DeepEqual(got[s1.ID], []string{"blue", "red"}) {
		t.Errorf("s1 tags = %v, want [blue red]", got[s1.ID])
	}
	if !reflect.DeepEqual(got[s2.ID], []string{"green"}) {
		t.Errorf("s2 tags = %v, want [green]", got[s2.ID])
	}
	if _, ok := got[s3.ID]; ok {
		t.Errorf("s3 should not be in result map (no tags), got %v", got[s3.ID])
	}

	// Empty input.
	empty, err := store.GetTagsBatch(nil)
	if err != nil {
		t.Fatalf("GetTagsBatch(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("GetTagsBatch(nil) = %v, want empty map", empty)
	}
}

// ── ListAllTags ──

func TestListAllTags_CountsAndOrder(t *testing.T) {
	store := mustOpenStore(t)
	s1 := testSession("ses_all_1")
	s2 := testSession("ses_all_2")
	s3 := testSession("ses_all_3")
	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save %s: %v", s.ID, err)
		}
	}
	if _, err := store.AddTags(s1.ID, []string{"common", "rare"}); err != nil {
		t.Fatalf("AddTags s1: %v", err)
	}
	if _, err := store.AddTags(s2.ID, []string{"common", "medium"}); err != nil {
		t.Fatalf("AddTags s2: %v", err)
	}
	if _, err := store.AddTags(s3.ID, []string{"common"}); err != nil {
		t.Fatalf("AddTags s3: %v", err)
	}

	got, err := store.ListAllTags()
	if err != nil {
		t.Fatalf("ListAllTags: %v", err)
	}
	want := []session.TagCount{
		{Tag: "common", Count: 3},
		{Tag: "medium", Count: 1},
		{Tag: "rare", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListAllTags = %v, want %v", got, want)
	}
}

// ── FilterSessionIDsByTags ──

func TestFilterSessionIDsByTags_AndSemantics(t *testing.T) {
	store := mustOpenStore(t)
	s1 := testSession("ses_f1")
	s2 := testSession("ses_f2")
	s3 := testSession("ses_f3")
	s4 := testSession("ses_f4") // no tags
	for _, s := range []*session.Session{s1, s2, s3, s4} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if _, err := store.AddTags(s1.ID, []string{"bug", "urgent"}); err != nil {
		t.Fatalf("AddTags s1: %v", err)
	}
	if _, err := store.AddTags(s2.ID, []string{"bug"}); err != nil {
		t.Fatalf("AddTags s2: %v", err)
	}
	if _, err := store.AddTags(s3.ID, []string{"urgent"}); err != nil {
		t.Fatalf("AddTags s3: %v", err)
	}

	all := []session.ID{s1.ID, s2.ID, s3.ID, s4.ID}

	// Single tag — bug → s1, s2.
	got, err := store.FilterSessionIDsByTags(all, []string{"bug"})
	if err != nil {
		t.Fatalf("Filter bug: %v", err)
	}
	sortIDs(got)
	if !reflect.DeepEqual(got, []session.ID{s1.ID, s2.ID}) {
		t.Errorf("filter [bug] = %v, want [s1 s2]", got)
	}

	// AND of two tags — only s1 has both.
	got, err = store.FilterSessionIDsByTags(all, []string{"bug", "urgent"})
	if err != nil {
		t.Fatalf("Filter and: %v", err)
	}
	if !reflect.DeepEqual(got, []session.ID{s1.ID}) {
		t.Errorf("filter [bug urgent] = %v, want [s1]", got)
	}

	// Normalization on input.
	got, err = store.FilterSessionIDsByTags(all, []string{"BUG", "Urgent"})
	if err != nil {
		t.Fatalf("Filter normalize: %v", err)
	}
	if !reflect.DeepEqual(got, []session.ID{s1.ID}) {
		t.Errorf("filter [BUG Urgent] = %v, want [s1]", got)
	}

	// Tag with no matches.
	got, err = store.FilterSessionIDsByTags(all, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("Filter none: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("filter [nonexistent] = %v, want empty", got)
	}

	// Empty tag list returns input unchanged.
	got, err = store.FilterSessionIDsByTags(all, nil)
	if err != nil {
		t.Fatalf("Filter empty tags: %v", err)
	}
	if !reflect.DeepEqual(got, all) {
		t.Errorf("filter [] = %v, want full input", got)
	}

	// Empty IDs list.
	got, err = store.FilterSessionIDsByTags(nil, []string{"bug"})
	if err != nil {
		t.Fatalf("Filter empty ids: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("filter empty ids = %v, want empty", got)
	}
}

func sortIDs(ids []session.ID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
