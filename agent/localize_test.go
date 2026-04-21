// ABOUTME: Tests for repository localization pre-processing.
// ABOUTME: Validates reference extraction, filesystem scan scoring, and injection capping.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, rel, contents string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestExtractRefs(t *testing.T) {
	refs := extractRefs(`Please fix the bug in auth.go where validateToken returns nil. Error: "token missing"`)

	foundPath := false
	for _, p := range refs.Paths {
		if p == "auth.go" {
			foundPath = true
		}
	}
	if !foundPath {
		t.Errorf("expected auth.go in paths, got %v", refs.Paths)
	}

	foundIdent := false
	for _, id := range refs.Identifiers {
		if id == "validateToken" {
			foundIdent = true
		}
	}
	if !foundIdent {
		t.Errorf("expected validateToken in identifiers, got %v", refs.Identifiers)
	}

	if len(refs.Phrases) == 0 {
		t.Errorf("expected at least one phrase, got none")
	}
}

func TestExtractRefs_VersionStringsIgnored(t *testing.T) {
	refs := extractRefs("Upgrade to v1.2.3 or 2.0.0 — see 3.4.5 release notes")
	for _, p := range refs.Paths {
		if p == "v1.2.3" || p == "2.0.0" || p == "3.4.5" {
			t.Errorf("version string %q should not be extracted as path", p)
		}
	}
}

func TestExtractRefs_EmptyPrompt(t *testing.T) {
	refs := extractRefs("")
	if len(refs.Paths)+len(refs.Identifiers)+len(refs.Phrases) != 0 {
		t.Errorf("expected empty refs for empty prompt, got %+v", refs)
	}
}

func TestLocalize_FindsFileByName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "auth.go", "package auth\n\nfunc validateToken(t string) error {\n\treturn nil\n}\n")
	writeFile(t, dir, "unrelated.go", "package foo\n")

	result := localize(context.Background(), dir, "Fix auth.go")
	if len(result.Candidates) == 0 {
		t.Fatalf("expected at least one candidate, got none")
	}
	if result.Candidates[0].Path != "auth.go" {
		t.Errorf("expected top candidate auth.go, got %s", result.Candidates[0].Path)
	}
	if !strings.Contains(result.Message, "auth.go") {
		t.Errorf("expected auth.go in injection message, got:\n%s", result.Message)
	}
}

func TestLocalize_FindsFileByErrorString(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "handler.go", "package x\n\n// Error: unexpected EOF\nfunc handle() {}\n")
	writeFile(t, dir, "noise.go", "package x\nfunc noise() {}\n")

	result := localize(context.Background(), dir, `I'm seeing "unexpected EOF" when parsing input`)
	if len(result.Candidates) == 0 {
		t.Fatalf("expected at least one candidate, got none")
	}
	foundHandler := false
	for _, c := range result.Candidates {
		if c.Path == "handler.go" {
			foundHandler = true
		}
	}
	if !foundHandler {
		t.Errorf("expected handler.go (contains error phrase), got %+v", result.Candidates)
	}
}

func TestLocalize_RespectsCap(t *testing.T) {
	dir := t.TempDir()
	// Create 20 files that all contain the prompt identifier "configValue"
	// and have enough content that injecting snippets for all of them would
	// exceed localizeMaxInjectBytes. This ensures both caps are exercised:
	// the 10-file cap and the 2KB byte cap.
	largeMatch := strings.Repeat("var configValue = 1\n", 64)
	for i := 0; i < 20; i++ {
		name := filepath.Join("pkg", fmt.Sprintf("config%02d.go", i))
		writeFile(t, dir, name, "package pkg\n"+largeMatch)
	}

	result := localize(context.Background(), dir, "update configValue across the repo")
	if len(result.Candidates) == 0 {
		t.Fatalf("expected matching candidates, got none")
	}
	if len(result.Candidates) > localizeMaxFiles {
		t.Errorf("expected at most %d candidates, got %d", localizeMaxFiles, len(result.Candidates))
	}
	if len(result.Message) > localizeMaxInjectBytes {
		t.Errorf("expected message <= %d bytes, got %d", localizeMaxInjectBytes, len(result.Message))
	}
}

func TestBuildInjection_PathOnlyFallbackWhenSnippetTooLarge(t *testing.T) {
	dir := t.TempDir()
	// Single file with a huge matching line — the snippet would exceed the cap,
	// so the injection should fall back to path-only form and still reference
	// the file.
	huge := strings.Repeat("configValue = 1; // padding padding padding padding padding\n", 80)
	writeFile(t, dir, "big.go", "package x\n"+huge)

	result := localize(context.Background(), dir, "update configValue")
	if len(result.Message) == 0 {
		t.Fatalf("expected non-empty message with path-only fallback, got empty")
	}
	if len(result.Message) > localizeMaxInjectBytes {
		t.Errorf("expected message <= %d bytes, got %d", localizeMaxInjectBytes, len(result.Message))
	}
	if !strings.Contains(result.Message, "big.go") {
		t.Errorf("expected big.go in message even under fallback, got:\n%s", result.Message)
	}
}

func TestExtractRefs_WindowsPaths(t *testing.T) {
	refs := extractRefs(`Fix the bug in src\main.go and also src\pkg\handler.go`)
	hasMain, hasHandler := false, false
	for _, p := range refs.Paths {
		if p == "src/main.go" {
			hasMain = true
		}
		if p == "src/pkg/handler.go" {
			hasHandler = true
		}
	}
	if !hasMain || !hasHandler {
		t.Errorf("expected normalized Unix paths from Windows-style input, got %v", refs.Paths)
	}
}

func TestExtractRefs_AcronymPrefixedIdentifiers(t *testing.T) {
	refs := extractRefs("The HTTPServer crashes when URLParser encounters a malformed input")
	hasHTTP, hasURL := false, false
	for _, id := range refs.Identifiers {
		if id == "HTTPServer" {
			hasHTTP = true
		}
		if id == "URLParser" {
			hasURL = true
		}
	}
	if !hasHTTP || !hasURL {
		t.Errorf("expected acronym-prefixed identifiers, got %v", refs.Identifiers)
	}
}

func TestExtractRefs_PhrasesDeduped(t *testing.T) {
	// Same phrase mentioned via quotes and an error-line pattern — it should
	// appear only once in refs.Phrases.
	refs := extractRefs(`Hit "token expired" on login. Error: token expired when retrying.`)
	count := 0
	for _, p := range refs.Phrases {
		if p == "token expired" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected phrase %q deduped to a single entry, got %d occurrences in %v", "token expired", count, refs.Phrases)
	}
}

func TestLocalize_SkipsDotfilesAndSecrets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "SECRET_TOKEN=abc123\nconfigValue=leak\n")
	writeFile(t, dir, "server.pem", "-----BEGIN PRIVATE KEY-----\nconfigValue leak\n-----END-----\n")
	writeFile(t, dir, "id_rsa", "configValue leak\n")
	writeFile(t, dir, "app.go", "package app\nvar configValue = 1\n")

	result := localize(context.Background(), dir, "inspect configValue")
	for _, c := range result.Candidates {
		base := filepath.Base(c.Path)
		if base == ".env" || base == "server.pem" || base == "id_rsa" {
			t.Errorf("sensitive file %q should have been skipped", c.Path)
		}
	}
	if !strings.Contains(result.Message, "app.go") {
		t.Errorf("expected app.go in message, got:\n%s", result.Message)
	}
}

func TestLocalize_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "auth.go", "package x\nfunc validateToken() {}\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := localize(ctx, dir, "Fix validateToken in auth.go")
	if len(result.Candidates) != 0 || result.Message != "" {
		t.Errorf("expected empty result on canceled context, got %+v", result)
	}
}

func TestLocalize_EmptyWhenNoRefs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.go", "package x\n")

	result := localize(context.Background(), dir, "please help")
	if len(result.Candidates) != 0 || result.Message != "" {
		t.Errorf("expected empty result for prompt with no refs, got %+v", result)
	}
}

func TestLocalize_SkipsDependencyDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "node_modules/auth.go", "package x\nvar validateToken = 1\n")
	writeFile(t, dir, "src/auth.go", "package x\nfunc validateToken() {}\n")

	result := localize(context.Background(), dir, "Fix validateToken in auth.go")
	for _, c := range result.Candidates {
		if strings.HasPrefix(c.Path, "node_modules/") {
			t.Errorf("node_modules file %q should have been skipped", c.Path)
		}
	}
}

func TestInitConversation_LocalizeDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Localize = false
	cfg.WorkingDir = t.TempDir()
	writeFile(t, cfg.WorkingDir, "auth.go", "package x\n")

	s := &Session{config: cfg}
	s.initConversation(context.Background(), "Fix the bug in auth.go")

	if len(s.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.messages))
	}
	userMsg := s.messages[1]
	text := ""
	for _, c := range userMsg.Content {
		text += c.Text
	}
	if text != "Fix the bug in auth.go" {
		t.Errorf("expected user message unchanged when Localize=false, got %q", text)
	}
}

func TestInitConversation_LocalizeEnabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Localize = true
	cfg.WorkingDir = t.TempDir()
	writeFile(t, cfg.WorkingDir, "auth.go", "package x\nfunc validateToken() {}\n")

	s := &Session{config: cfg}
	s.initConversation(context.Background(), "Fix the bug in auth.go")

	if len(s.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.messages))
	}
	text := ""
	for _, c := range s.messages[1].Content {
		text += c.Text
	}
	if !strings.Contains(text, "auth.go") {
		t.Errorf("expected injected localization block referencing auth.go, got:\n%s", text)
	}
	if !strings.Contains(text, "Fix the bug in auth.go") {
		t.Errorf("expected original user prompt preserved, got:\n%s", text)
	}
}
