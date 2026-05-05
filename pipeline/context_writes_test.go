package pipeline

import (
	"reflect"
	"testing"
)

func TestParseDeclaredKeys(t *testing.T) {
	got := ParseDeclaredKeys(" milestone_id, files, ,est_hours ")
	want := []string{"milestone_id", "files", "est_hours"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseDeclaredKeys() = %#v, want %#v", got, want)
	}
}

func TestExtractDeclaredWrites(t *testing.T) {
	updates, extras, err := ExtractDeclaredWrites(
		[]string{"milestone_id", "files", "est_hours"},
		`{"milestone_id":"m1-auth","files":["a.go","b.go"],"est_hours":4,"typo":"x"}`,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantUpdates := map[string]string{
		"milestone_id": "m1-auth",
		"files":        `["a.go","b.go"]`,
		"est_hours":    "4",
	}
	if !reflect.DeepEqual(updates, wantUpdates) {
		t.Fatalf("updates = %#v, want %#v", updates, wantUpdates)
	}
	if !reflect.DeepEqual(extras, []string{"typo"}) {
		t.Fatalf("extras = %#v, want %#v", extras, []string{"typo"})
	}
}

func TestExtractDeclaredWritesMissingKey(t *testing.T) {
	_, _, err := ExtractDeclaredWrites([]string{"a", "b"}, `{"a":"x"}`)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestExtractDeclaredWritesInvalidJSON(t *testing.T) {
	_, _, err := ExtractDeclaredWrites([]string{"a"}, `not-json`)
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestExtractDeclaredWritesCompactsNonStringValues(t *testing.T) {
	updates, _, err := ExtractDeclaredWrites(
		[]string{"files", "meta"},
		`{ "files": [ "a.go" , "b.go" ], "meta": { "x" : 1 } }`,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := updates["files"]; got != `["a.go","b.go"]` {
		t.Fatalf("files = %q, want compact json array", got)
	}
	if got := updates["meta"]; got != `{"x":1}` {
		t.Fatalf("meta = %q, want compact json object", got)
	}
}

// --- ExtractJSONFromText tests ---

func TestExtractJSONFromText_FencedJSONBlock(t *testing.T) {
	text := "Here is the analysis:\n```json\n{\"spec_analysis\": \"the summary\"}\n```\nDone."
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true for fenced json block")
	}
	if got != `{"spec_analysis": "the summary"}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONFromText_FencedBlockNoLanguage(t *testing.T) {
	text := "Result:\n```\n{\"key\": \"val\"}\n```\n"
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true for fenced block without language tag")
	}
	if got != `{"key": "val"}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONFromText_OutermostBraces(t *testing.T) {
	text := `Done. Here is the result: {"spec_analysis": "summary of findings"} and that is all.`
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true for outermost braces")
	}
	if got != `{"spec_analysis": "summary of findings"}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONFromText_OutermostBracesNested(t *testing.T) {
	text := `Output: {"key": {"nested": true}, "list": [1,2]} end`
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true for nested braces")
	}
	if got != `{"key": {"nested": true}, "list": [1,2]}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONFromText_NoParsableJSON(t *testing.T) {
	text := "Done — `.ai/spec_analysis.md` has been created.\n\nSummary:\n- 104 functional requirements\n- 11 components"
	_, ok := ExtractJSONFromText(text)
	if ok {
		t.Fatal("expected ok=false for pure prose")
	}
}

func TestExtractJSONFromText_BracesButInvalidJSON(t *testing.T) {
	text := "I wrote {file1} and {file2} to disk."
	_, ok := ExtractJSONFromText(text)
	if ok {
		t.Fatal("expected ok=false when braces don't contain valid JSON")
	}
}

func TestExtractJSONFromText_FencedPreferredOverBraces(t *testing.T) {
	text := "Preamble with {\"wrong\": true} inline.\n```json\n{\"right\": true}\n```\nEnd."
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != `{"right": true}` {
		t.Fatalf("expected fenced block to win, got %q", got)
	}
}

func TestExtractJSONFromText_EmptyString(t *testing.T) {
	_, ok := ExtractJSONFromText("")
	if ok {
		t.Fatal("expected ok=false for empty string")
	}
}
