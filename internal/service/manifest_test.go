package service

import (
	"reflect"
	"testing"
)

func TestParseFileManifest_JSONArray(t *testing.T) {
	got, err := ParseFileManifest([]byte(`["a.go", "b.go", "dir/c.go"]`))
	if err != nil {
		t.Fatalf("ParseFileManifest() error: %v", err)
	}
	want := []string{"a.go", "b.go", "dir/c.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFileManifest_JSONArrayLeadingWhitespace(t *testing.T) {
	got, err := ParseFileManifest([]byte("  \n\t[\"a.go\"]\n"))
	if err != nil {
		t.Fatalf("ParseFileManifest() error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a.go"}) {
		t.Errorf("got %v, want [a.go]", got)
	}
}

func TestParseFileManifest_JSONMalformed(t *testing.T) {
	_, err := ParseFileManifest([]byte(`["a.go", "b.go"`))
	if err == nil {
		t.Fatal("expected error for malformed JSON array, got nil")
	}
}

func TestParseFileManifest_LinesBasic(t *testing.T) {
	got, err := ParseFileManifest([]byte("a.go\nb.go\ndir/c.go\n"))
	if err != nil {
		t.Fatalf("ParseFileManifest() error: %v", err)
	}
	want := []string{"a.go", "b.go", "dir/c.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFileManifest_LinesSkipBlanksAndComments(t *testing.T) {
	content := "a.go\n\n  # a comment\n   \nb.go\n# trailing\n"
	got, err := ParseFileManifest([]byte(content))
	if err != nil {
		t.Fatalf("ParseFileManifest() error: %v", err)
	}
	want := []string{"a.go", "b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFileManifest_LinesTrimWhitespace(t *testing.T) {
	got, err := ParseFileManifest([]byte("  a.go  \n\tb.go\t\n"))
	if err != nil {
		t.Fatalf("ParseFileManifest() error: %v", err)
	}
	want := []string{"a.go", "b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
