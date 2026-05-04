// ABOUTME: Unit tests for dispatch_sprints — JSONL parsing, validation, and aggregation.
// ABOUTME: Uses an inline mock Completer to drive the per-sprint author+audit loop without real LLM calls.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

// mockCompleter cycles through a list of canned responses on each Complete call.
type mockCompleter struct {
	responses []*llm.Response
	calls     int32
	failOn    int // 0-indexed call number to fail on; -1 means never
}

func (m *mockCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	idx := atomic.AddInt32(&m.calls, 1) - 1
	if m.failOn >= 0 && int(idx) == m.failOn {
		return nil, errors.New("mock: induced failure")
	}
	if int(idx) >= len(m.responses) {
		return m.responses[len(m.responses)-1], nil
	}
	return m.responses[idx], nil
}

func authorResponse(body string) *llm.Response {
	return &llm.Response{
		ID:      "a",
		Message: llm.AssistantMessage(body),
		Usage:   llm.Usage{InputTokens: 100, OutputTokens: 50},
	}
}

func auditPassResponse() *llm.Response {
	return &llm.Response{
		ID:      "b",
		Message: llm.AssistantMessage("AUDIT-VERDICT: PASS"),
		Usage:   llm.Usage{InputTokens: 80, OutputTokens: 5},
	}
}

func writeJSONL(t *testing.T, dir string, lines []dispatchEntry) string {
	t.Helper()
	path := filepath.Join(dir, "plan.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close plan: %v", err)
		}
	}()
	for _, e := range lines {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := fmt.Fprintln(f, string(b)); err != nil {
			t.Fatalf("write plan: %v", err)
		}
	}
	return path
}

func writeContract(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "contract.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	return path
}

func TestDispatchSprintsHappyPath(t *testing.T) {
	dir := t.TempDir()
	contractPath := writeContract(t, dir, "# Contract\n\nstack: python\n")

	entries := []dispatchEntry{
		{Path: "SPRINT-001.md", Description: "Sprint 1 — foundation. " + strings.Repeat("x", 250)},
		{Path: "SPRINT-002.md", Description: "Sprint 2 — additive notes. " + strings.Repeat("y", 250)},
		{Path: "SPRINT-003.md", Description: "Sprint 3 — additive tags. " + strings.Repeat("z", 250)},
	}
	planPath := writeJSONL(t, dir, entries)

	// 6 LLM calls expected: 3 sprints × (author + audit).
	mock := &mockCompleter{
		failOn: -1,
		responses: []*llm.Response{
			authorResponse("# Sprint 001 — Foundation (enriched spec)\n\n## Scope\n..."),
			auditPassResponse(),
			authorResponse("# Sprint 002 — Notes (enriched spec)\n\n## Scope\n..."),
			auditPassResponse(),
			authorResponse("# Sprint 003 — Tags (enriched spec)\n\n## Scope\n..."),
			auditPassResponse(),
		},
	}

	writer := NewWriteEnrichedSprintTool(mock,
		WithSprintWriterModel("mock-sonnet"),
		WithSprintWriterWorkDir(dir),
	)
	dispatch := NewDispatchSprintsTool(writer, dir)

	args := map[string]any{
		"descriptions_file": planPath,
		"contract_file":     contractPath,
		"output_dir":        filepath.Join(dir, "sprints"),
	}
	argsJSON, _ := json.Marshal(args)

	out, err := dispatch.Execute(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if !strings.Contains(out, "dispatched=3") || !strings.Contains(out, "PASS=3") {
		t.Errorf("summary missing expected counts: %s", out)
	}
	if mock.calls != 6 {
		t.Errorf("expected 6 LLM calls (3 sprints × 2 passes), got %d", mock.calls)
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(dir, "sprints", e.Path)); err != nil {
			t.Errorf("expected sprint file %s: %v", e.Path, err)
		}
	}
}

func TestDispatchSprintsRejectsInvalidJSONL(t *testing.T) {
	dir := t.TempDir()
	writeContract(t, dir, "contract")

	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"malformed json", "{not json}\n", "parse"},
		{"missing path", `{"description":"x"}` + "\n", "missing path"},
		{"invalid path", `{"path":"sprint1.md","description":"hello world hello world hello world"}` + "\n", "invalid path"},
		{"empty description", `{"path":"SPRINT-001.md","description":""}` + "\n", "empty description"},
		{"empty file", "", "no entries"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := filepath.Join(dir, tc.name+".jsonl")
			if err := os.WriteFile(plan, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			mock := &mockCompleter{failOn: -1}
			writer := NewWriteEnrichedSprintTool(mock, WithSprintWriterWorkDir(dir))
			dispatch := NewDispatchSprintsTool(writer, dir)

			args := map[string]any{
				"descriptions_file": plan,
				"contract_file":     filepath.Join(dir, "contract.md"),
			}
			argsJSON, _ := json.Marshal(args)

			_, err := dispatch.Execute(context.Background(), argsJSON)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
			if mock.calls != 0 {
				t.Errorf("expected 0 LLM calls when validation fails up-front, got %d", mock.calls)
			}
		})
	}
}

func TestDispatchSprintsContinuesOnFailure(t *testing.T) {
	dir := t.TempDir()
	contractPath := writeContract(t, dir, "contract body")

	entries := []dispatchEntry{
		{Path: "SPRINT-001.md", Description: "s1 " + strings.Repeat("a", 250)},
		{Path: "SPRINT-002.md", Description: "s2 " + strings.Repeat("b", 250)},
		{Path: "SPRINT-003.md", Description: "s3 " + strings.Repeat("c", 250)},
	}
	planPath := writeJSONL(t, dir, entries)

	// Fail on call index 2 → that's the AUTHOR pass for sprint 002.
	// Sprint 001 succeeds (calls 0,1), sprint 002 fails (call 2),
	// sprint 003 should still proceed (calls 3 author, 4 audit).
	mock := &mockCompleter{
		failOn: 2,
		responses: []*llm.Response{
			authorResponse("# Sprint 001\n## Scope\nx"),
			auditPassResponse(),
			nil, // unused, the failOn intercepts
			authorResponse("# Sprint 003\n## Scope\nx"),
			auditPassResponse(),
		},
	}

	writer := NewWriteEnrichedSprintTool(mock, WithSprintWriterWorkDir(dir))
	dispatch := NewDispatchSprintsTool(writer, dir)

	args := map[string]any{
		"descriptions_file": planPath,
		"contract_file":     contractPath,
		"output_dir":        filepath.Join(dir, "sprints"),
	}
	argsJSON, _ := json.Marshal(args)

	out, err := dispatch.Execute(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("non-strict mode should not error on per-sprint failure, got: %v", err)
	}
	if !strings.Contains(out, "failures=1") {
		t.Errorf("expected failures=1 in summary: %s", out)
	}
	if !strings.Contains(out, "SPRINT-002.md: FAIL") {
		t.Errorf("expected sprint 002 marked FAIL: %s", out)
	}
	// Sprint 001 and 003 should be on disk; sprint 002 should NOT.
	for _, p := range []string{"SPRINT-001.md", "SPRINT-003.md"} {
		if _, err := os.Stat(filepath.Join(dir, "sprints", p)); err != nil {
			t.Errorf("expected %s written: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sprints", "SPRINT-002.md")); err == nil {
		t.Errorf("did not expect SPRINT-002.md to be written after failure")
	}
}

func TestDispatchSprintsStrictHaltsOnFailure(t *testing.T) {
	dir := t.TempDir()
	contractPath := writeContract(t, dir, "contract body")

	entries := []dispatchEntry{
		{Path: "SPRINT-001.md", Description: "s1 " + strings.Repeat("a", 250)},
		{Path: "SPRINT-002.md", Description: "s2 " + strings.Repeat("b", 250)},
		{Path: "SPRINT-003.md", Description: "s3 " + strings.Repeat("c", 250)},
	}
	planPath := writeJSONL(t, dir, entries)

	// Fail on first call → first author pass for sprint 001.
	mock := &mockCompleter{failOn: 0}

	writer := NewWriteEnrichedSprintTool(mock, WithSprintWriterWorkDir(dir))
	dispatch := NewDispatchSprintsTool(writer, dir)

	args := map[string]any{
		"descriptions_file": planPath,
		"contract_file":     contractPath,
		"output_dir":        filepath.Join(dir, "sprints"),
		"strict":            true,
	}
	argsJSON, _ := json.Marshal(args)

	_, err := dispatch.Execute(context.Background(), argsJSON)
	if err == nil {
		t.Fatal("strict mode should error on per-sprint failure")
	}
	if !strings.Contains(err.Error(), "strict halt") {
		t.Errorf("expected strict-halt error, got: %v", err)
	}
	// Subsequent sprints should NOT have been attempted (only 1 call total).
	if mock.calls != 1 {
		t.Errorf("expected 1 LLM call before strict halt, got %d", mock.calls)
	}
}

// retryBehavior describes one call's response: either an error (returned to
// the caller) or a Response (returned as success). Used by retryMockCompleter
// to script per-call outcomes.
type retryBehavior struct {
	err  error
	resp *llm.Response
}

// retryMockCompleter returns a different (response, error) per call based on
// its scripted behaviors. After the script is exhausted, returns fallback
// for every subsequent call. Used to simulate transient-then-success flows
// for runOneWithRetry tests.
type retryMockCompleter struct {
	behaviors []retryBehavior
	fallback  *llm.Response
	calls     int32
}

func (m *retryMockCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	idx := int(atomic.AddInt32(&m.calls, 1) - 1)
	if idx < len(m.behaviors) {
		b := m.behaviors[idx]
		if b.err != nil {
			return nil, b.err
		}
		return b.resp, nil
	}
	return m.fallback, nil
}

// TestRunOneWithRetry_TransientThenSucceeds verifies that two consecutive
// retryable provider errors (RateLimitError, ServerError) are retried, and
// the third attempt's success is returned to the caller. The dispatch tool
// uses a no-op backoff in tests to keep the wall time fast.
func TestRunOneWithRetry_TransientThenSucceeds(t *testing.T) {
	dir := t.TempDir()
	contractPath := writeContract(t, dir, "contract body")
	entries := []dispatchEntry{
		{Path: "SPRINT-001.md", Description: "s1 " + strings.Repeat("a", 250)},
	}
	planPath := writeJSONL(t, dir, entries)

	// Calls 0-1: retryable errors → runOneWithRetry should retry.
	// Call 2: real author response → RunOne's author succeeds.
	// Call 3: real audit response → RunOne returns successfully.
	mock := &retryMockCompleter{
		behaviors: []retryBehavior{
			{err: &llm.RateLimitError{ProviderError: llm.ProviderError{StatusCode: 429}}},
			{err: &llm.ServerError{ProviderError: llm.ProviderError{StatusCode: 503}}},
			{resp: authorResponse("# Sprint 001 — Foundation\n\n## Scope\nBody.")},
			{resp: auditPassResponse()},
		},
	}

	writer := NewWriteEnrichedSprintTool(mock, WithSprintWriterWorkDir(dir))
	dispatch := NewDispatchSprintsTool(writer, dir)
	dispatch.retryBackoff = func(int) time.Duration { return 1 * time.Millisecond } // fast test

	args := map[string]any{
		"descriptions_file": planPath,
		"contract_file":     contractPath,
		"output_dir":        filepath.Join(dir, "sprints"),
	}
	argsJSON, _ := json.Marshal(args)

	out, err := dispatch.Execute(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("dispatch should succeed after retries, got: %v", err)
	}
	if !strings.Contains(out, "PASS=1") {
		t.Errorf("expected sprint to land as PASS after retries: %s", out)
	}
	// Total Complete calls: 2 retried failures + 1 author success + 1 audit success = 4.
	if mock.calls != 4 {
		t.Errorf("expected 4 Complete calls (2 retries + author + audit), got %d", mock.calls)
	}
}

// TestRunOneWithRetry_NonRetryableErrorAborts verifies that a non-retryable
// error (AuthenticationError) bubbles back to dispatch_sprints on the first
// attempt without triggering retries.
func TestRunOneWithRetry_NonRetryableErrorAborts(t *testing.T) {
	dir := t.TempDir()
	contractPath := writeContract(t, dir, "contract body")
	entries := []dispatchEntry{
		{Path: "SPRINT-001.md", Description: "s1 " + strings.Repeat("a", 250)},
	}
	planPath := writeJSONL(t, dir, entries)

	// Call 0: AuthenticationError (Retryable=false). runOneWithRetry should
	// return the error immediately rather than retrying.
	mock := &retryMockCompleter{
		behaviors: []retryBehavior{
			{err: &llm.AuthenticationError{ProviderError: llm.ProviderError{StatusCode: 401}}},
		},
	}

	writer := NewWriteEnrichedSprintTool(mock, WithSprintWriterWorkDir(dir))
	dispatch := NewDispatchSprintsTool(writer, dir)
	dispatch.retryBackoff = func(int) time.Duration { return 1 * time.Millisecond }

	args := map[string]any{
		"descriptions_file": planPath,
		"contract_file":     contractPath,
		"output_dir":        filepath.Join(dir, "sprints"),
	}
	argsJSON, _ := json.Marshal(args)

	out, err := dispatch.Execute(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("non-strict dispatch shouldn't error on per-sprint failure, got: %v", err)
	}
	if !strings.Contains(out, "failures=1") {
		t.Errorf("expected the non-retryable failure to surface in summary: %s", out)
	}
	// Only 1 Complete call — non-retryable errors do not retry.
	if mock.calls != 1 {
		t.Errorf("expected 1 Complete call (no retry on non-retryable error), got %d", mock.calls)
	}
}

// TestExecute_PatchedPartial verifies that when the audit emits multiple SR
// blocks where some apply and others fail, Execute() reports
// `audit=PATCHED-PARTIAL` with `patches=N` (where N is the count that landed)
// rather than falling back to the unaudited draft.
func TestExecute_PatchedPartial(t *testing.T) {
	dir := t.TempDir()
	contractPath := writeContract(t, dir, "contract body")
	outputDir := filepath.Join(dir, "sprints")

	// Author draft contains the text "alpha" but NOT "this text is not in draft".
	// The audit emits TWO blocks: block 0 patches "alpha" (will apply); block 1's
	// SEARCH does not exist anywhere in the draft (will be skipped). With partial-
	// apply, the verdict should be PATCHED-PARTIAL with 1 block landed.
	draft := "# Sprint 001\n\n## Scope\nBody references alpha here.\n\nMore content.\n"
	auditResponse := `AUDIT-VERDICT: PATCHED
<<<<<<< SEARCH
alpha
=======
ALPHA
>>>>>>> REPLACE
<<<<<<< SEARCH
this text is not in draft and never will be
=======
zzz
>>>>>>> REPLACE`

	mock := &retryMockCompleter{
		behaviors: []retryBehavior{
			{resp: authorResponse(draft)},
			{resp: &llm.Response{ID: "x", Message: llm.AssistantMessage(auditResponse), Usage: llm.Usage{InputTokens: 50, OutputTokens: 30}}},
		},
	}

	writer := NewWriteEnrichedSprintTool(mock, WithSprintWriterWorkDir(dir))
	args := map[string]any{
		"path":          "SPRINT-001.md",
		"description":   "Sprint 001 description " + strings.Repeat("x", 200),
		"contract_file": contractPath,
		"output_dir":    outputDir,
	}
	argsJSON, _ := json.Marshal(args)

	out, err := writer.Execute(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// Verdict should be PATCHED-PARTIAL because 1 of 2 blocks could not match.
	if !strings.Contains(out, "audit=PATCHED-PARTIAL") {
		t.Errorf("expected PATCHED-PARTIAL verdict in summary: %s", out)
	}
	// Exactly 1 patch landed (block 0 matched, block 1 didn't).
	if !strings.Contains(out, "patches=1") {
		t.Errorf("expected patches=1 in summary: %s", out)
	}
	// Verify the file on disk contains the patched form (ALPHA, not alpha).
	written, err := os.ReadFile(filepath.Join(outputDir, "SPRINT-001.md"))
	if err != nil {
		t.Fatalf("could not read written sprint: %v", err)
	}
	if !strings.Contains(string(written), "ALPHA") {
		t.Errorf("expected partially-patched content (ALPHA) on disk, got %q", string(written))
	}
	if strings.Contains(string(written), "zzz") {
		t.Errorf("did not expect skipped block's REPLACE text on disk, got %q", string(written))
	}
}
