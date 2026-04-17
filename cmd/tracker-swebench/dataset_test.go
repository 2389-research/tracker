// ABOUTME: Tests for SWE-bench JSONL dataset parsing.
// ABOUTME: Covers LoadDataset, Instance field mapping, and AgentPrompt generation.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempJSONL(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dataset.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}

func TestLoadDataset(t *testing.T) {
	jsonl := `{"instance_id":"django__django-11099","repo":"django/django","base_commit":"abc123","problem_statement":"Fix the thing","hints_text":"Check models.py","version":"2.2","environment_setup_commit":"def456"}
{"instance_id":"sympy__sympy-12345","repo":"sympy/sympy","base_commit":"bbb222","problem_statement":"Another bug","hints_text":"","version":"1.6","environment_setup_commit":"ccc333"}`

	path := writeTempJSONL(t, jsonl)
	instances, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset returned error: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	first := instances[0]
	if first.InstanceID != "django__django-11099" {
		t.Errorf("InstanceID: got %q, want %q", first.InstanceID, "django__django-11099")
	}
	if first.Repo != "django/django" {
		t.Errorf("Repo: got %q, want %q", first.Repo, "django/django")
	}
	if first.BaseCommit != "abc123" {
		t.Errorf("BaseCommit: got %q, want %q", first.BaseCommit, "abc123")
	}
	if first.ProblemStatement != "Fix the thing" {
		t.Errorf("ProblemStatement: got %q, want %q", first.ProblemStatement, "Fix the thing")
	}
	if first.HintsText != "Check models.py" {
		t.Errorf("HintsText: got %q, want %q", first.HintsText, "Check models.py")
	}
	if first.Version != "2.2" {
		t.Errorf("Version: got %q, want %q", first.Version, "2.2")
	}
	if first.EnvSetupCommit != "def456" {
		t.Errorf("EnvSetupCommit: got %q, want %q", first.EnvSetupCommit, "def456")
	}

	second := instances[1]
	if second.InstanceID != "sympy__sympy-12345" {
		t.Errorf("second InstanceID: got %q, want %q", second.InstanceID, "sympy__sympy-12345")
	}
	if second.HintsText != "" {
		t.Errorf("second HintsText: expected empty, got %q", second.HintsText)
	}
}

func TestLoadDataset_EmptyFile(t *testing.T) {
	path := writeTempJSONL(t, "")
	instances, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset on empty file returned error: %v", err)
	}
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

func TestLoadDataset_BlankLines(t *testing.T) {
	jsonl := `{"instance_id":"a","repo":"x/x","base_commit":"1","problem_statement":"p","hints_text":"","version":"1","environment_setup_commit":"2"}

{"instance_id":"b","repo":"y/y","base_commit":"3","problem_statement":"q","hints_text":"","version":"2","environment_setup_commit":"4"}
`
	path := writeTempJSONL(t, jsonl)
	instances, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset with blank lines returned error: %v", err)
	}
	if len(instances) != 2 {
		t.Errorf("expected 2 instances (blank lines skipped), got %d", len(instances))
	}
}

func TestLoadDataset_BadJSON(t *testing.T) {
	jsonl := `{"instance_id":"ok","repo":"a/b","base_commit":"1","problem_statement":"x","hints_text":"","version":"1","environment_setup_commit":"2"}
not valid json at all`

	path := writeTempJSONL(t, jsonl)
	_, err := LoadDataset(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	// Error message should reference the line number.
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("expected error to mention line 2, got: %v", err)
	}
}

func TestInstance_AgentPrompt(t *testing.T) {
	t.Run("without hints", func(t *testing.T) {
		inst := Instance{
			ProblemStatement: "Fix the memory leak",
			HintsText:        "",
		}
		got := inst.AgentPrompt()
		if got != "Fix the memory leak" {
			t.Errorf("AgentPrompt without hints: got %q, want %q", got, "Fix the memory leak")
		}
	})

	t.Run("with hints", func(t *testing.T) {
		inst := Instance{
			ProblemStatement: "Fix the memory leak",
			HintsText:        "Look at pool.go",
		}
		got := inst.AgentPrompt()
		want := "Fix the memory leak\n\n## Hints\n\nLook at pool.go"
		if got != want {
			t.Errorf("AgentPrompt with hints: got %q, want %q", got, want)
		}
	})
}

func TestInstance_RepoURL(t *testing.T) {
	inst := Instance{Repo: "django/django"}
	got := inst.RepoURL()
	want := "https://github.com/django/django.git"
	if got != want {
		t.Errorf("RepoURL: got %q, want %q", got, want)
	}
}

func TestValidateInstanceID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"django__django-11099", true},
		{"sympy__sympy-12345", true},
		{"scikit-learn__scikit-learn-9876", true},
		{"", false},
		{"../../etc/passwd", false},
		{"foo;rm -rf /", false},
		{"foo bar", false},
		{"a/b", false},
	}
	for _, tt := range tests {
		err := validateInstanceID(tt.id)
		if tt.valid && err != nil {
			t.Errorf("validateInstanceID(%q) = %v, want nil", tt.id, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("validateInstanceID(%q) = nil, want error", tt.id)
		}
	}
}
