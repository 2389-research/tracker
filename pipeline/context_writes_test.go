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
