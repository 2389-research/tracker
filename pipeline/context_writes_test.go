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

// Regression: the previous extractFencedJSON stopped at the first ``` fence,
// so a non-JSON preamble fence (```text or ```bash) ahead of the real
// ```json block silently disabled extraction. CodeRabbit pr-feedback finding.
func TestExtractJSONFromText_MultipleFences_FirstNotJSON(t *testing.T) {
	text := "Preamble:\n```text\nthis is not json\n```\n\nReal answer:\n```json\n{\"k\": \"v\"}\n```\n"
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true; second fence is valid JSON")
	}
	if got != `{"k": "v"}` {
		t.Fatalf("got %q, want second fence content", got)
	}
}

// Regression: stray backticks in prose before the real fenced block used to
// cause the extractor to read between the stray fence and the real opening
// fence and give up. The iterating implementation handles it.
func TestExtractJSONFromText_StrayBackticksBeforeFence(t *testing.T) {
	text := "We use ``` to denote code in this codebase. The result:\n```json\n{\"answer\": 42}\n```\n"
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != `{"answer": 42}` {
		t.Fatalf("got %q, want fence content", got)
	}
}

// extractBracedJSON should pick the first balanced top-level JSON object,
// not the first-`{`/last-`}` shortcut. Prose that contains stray brace
// pairs around real JSON used to defeat the previous outermost-brace
// strategy because the substring spanned all of it and failed to parse.
func TestExtractJSONFromText_BracedSpanWithStrayPairs(t *testing.T) {
	text := `Wrote {file1} and {file2}, then produced {"x": 1, "y": "z"}. Done.`
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true; a real JSON object exists in the prose")
	}
	if got != `{"x": 1, "y": "z"}` {
		t.Fatalf("got %q, want the real JSON object", got)
	}
}

// Top-level JSON arrays and scalars should NOT be accepted — the writes
// contract is "JSON object". This pins the rejection so a future change to
// json.Unmarshal target type doesn't silently broaden acceptance.
func TestExtractJSONFromText_RejectsArrayAndScalar(t *testing.T) {
	for _, in := range []string{`[{"a":1}]`, `"hello"`, `42`, `null`, `true`} {
		if _, ok := ExtractJSONFromText(in); ok {
			t.Errorf("expected ok=false for non-object %q", in)
		}
	}
}

// Multiple complete JSON objects on separate lines (NDJSON-ish): the
// previous outermost-brace strategy spanned all of them and returned "".
// The balanced-brace scan returns the FIRST parseable object — better than
// silently dropping everything.
func TestExtractJSONFromText_NDJSONReturnsFirstObject(t *testing.T) {
	text := "{\"a\":1}\n{\"b\":2}\n"
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected first object to be returned")
	}
	if got != `{"a":1}` {
		t.Fatalf("got %q, want first object", got)
	}
}

// Braces inside string values must not throw off depth counting.
func TestExtractJSONFromText_BracesInsideStringValue(t *testing.T) {
	text := `Done: {"path": "/tmp/dir/{name}.txt", "ok": true} extra`
	got, ok := ExtractJSONFromText(text)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != `{"path": "/tmp/dir/{name}.txt", "ok": true}` {
		t.Fatalf("got %q", got)
	}
}
