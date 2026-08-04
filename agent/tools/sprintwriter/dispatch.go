// ABOUTME: Tool that dispatches enriched-sprint generation across a JSONL plan.
// ABOUTME: Reads {path, description} per line and invokes WriteEnrichedSprintTool.RunOne for each, returning an aggregate summary.
package sprintwriter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

// DispatchSprintsTool reads a JSONL plan and runs the per-sprint author+audit
// pipeline once per line. The loop is mechanical — there is no LLM agency
// between sprints. The contract file is loaded once and shared across all
// sprints.
type DispatchSprintsTool struct {
	inner   *WriteEnrichedSprintTool
	workDir string

	// retryBackoff returns the wait duration after a retryable failure on the
	// given attempt number (1-indexed). Defaults to attempt²·second if nil
	// (1s, 4s, 9s, ...). Tests override with a small constant.
	retryBackoff func(attempt int) time.Duration
}

// NewDispatchSprintsTool wraps a WriteEnrichedSprintTool with a deterministic
// JSONL-driven loop.
func NewDispatchSprintsTool(inner *WriteEnrichedSprintTool, workDir string) *DispatchSprintsTool {
	return &DispatchSprintsTool{inner: inner, workDir: workDir}
}

func (t *DispatchSprintsTool) Name() string { return "dispatch_sprints" }

// IsTerminal flags this tool as the terminal step in the architect agent's
// session. When dispatch_sprints succeeds, the agent runtime ends the session
// immediately — there is no meaningful follow-up for the model. JSONL/contract
// validation errors still bubble back as tool errors so the agent can retry.
func (t *DispatchSprintsTool) IsTerminal() bool { return true }

func (t *DispatchSprintsTool) Description() string {
	return "Dispatch enriched-sprint generation for every line of a JSONL plan. " +
		"Reads `.ai/sprint_descriptions.jsonl` (one JSON object per line: " +
		"`{path: \"SPRINT-NNN.md\", description: str}`), reads `.ai/contract.md` once, " +
		"and runs the author+audit pipeline once per sprint. Returns an aggregate summary. " +
		"Call this AFTER writing the contract and the descriptions JSONL — once, with no per-sprint args."
}

func (t *DispatchSprintsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"descriptions_file": {
				"type": "string",
				"description": "Path to the JSONL plan. Defaults to .ai/sprint_descriptions.jsonl"
			},
			"contract_file": {
				"type": "string",
				"description": "Path to the project contract. Defaults to .ai/contract.md"
			},
			"output_dir": {
				"type": "string",
				"description": "Directory to write sprint files. Defaults to .ai/sprints"
			},
			"strict": {
				"type": "boolean",
				"description": "If true, abort on the first per-sprint failure. Default false (continue and report)."
			}
		}
	}`)
}

type dispatchEntry struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}

// dispatchArgs are the parsed Execute arguments, shared by parseDispatchArgs
// and prepareDispatch.
type dispatchArgs struct {
	DescriptionsFile string `json:"descriptions_file"`
	ContractFile     string `json:"contract_file"`
	OutputDir        string `json:"output_dir"`
	Strict           bool   `json:"strict"`
}

var sprintPathRE = regexp.MustCompile(`^SPRINT-\d{3}\.md$`)

// closeDispatchFile closes f, logging (but not failing on) a close error.
func closeDispatchFile(f *os.File, path string) {
	if cerr := f.Close(); cerr != nil {
		fmt.Fprintf(os.Stderr, "dispatch_sprints: close %s: %v\n", path, cerr)
	}
}

// parseDispatchLine parses one JSONL line. A blank line yields ok=false with a
// nil error (skip); a malformed or invalid line yields a non-nil error.
func parseDispatchLine(line string, lineno int) (dispatchEntry, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return dispatchEntry{}, false, nil
	}
	var e dispatchEntry
	if err := json.Unmarshal([]byte(trimmed), &e); err != nil {
		return e, false, fmt.Errorf("line %d: parse: %w", lineno, err)
	}
	if e.Path == "" {
		return e, false, fmt.Errorf("line %d: missing path", lineno)
	}
	if !sprintPathRE.MatchString(e.Path) {
		return e, false, fmt.Errorf("line %d: invalid path %q (want SPRINT-NNN.md)", lineno, e.Path)
	}
	if strings.TrimSpace(e.Description) == "" {
		return e, false, fmt.Errorf("line %d: empty description", lineno)
	}
	return e, true, nil
}

// readDispatchPlan parses a JSONL file into validated entries. Returns an error
// on the first malformed or invalid line so problems surface before any
// expensive LLM calls fire.
func readDispatchPlan(path string) ([]dispatchEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer closeDispatchFile(f, path)

	var entries []dispatchEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024) // allow long descriptions
	lineno := 0
	for sc.Scan() {
		lineno++
		e, ok, perr := parseDispatchLine(sc.Text(), lineno)
		if perr != nil {
			return nil, perr
		}
		if ok {
			entries = append(entries, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no entries in %s", path)
	}
	return entries, nil
}

// dispatchTotals accumulates per-sprint outcomes across the dispatch loop.
type dispatchTotals struct {
	perSprint []string
	failures  []string
	passes    int
	patched   int
	fallbacks int
	totalIn   int
	totalOut  int
}

// recordVerdict tallies one successful sprint's verdict into the counters.
func (tot *dispatchTotals) recordVerdict(v string) {
	switch {
	case v == "PASS":
		tot.passes++
	case v == "PATCHED" || v == "PATCHED-PARTIAL":
		tot.patched++
	case strings.HasPrefix(v, "PASS-FALLBACK"):
		tot.fallbacks++
	}
}

// ensureConfigured guards against a misregistered tool (nil receiver or nil
// inner) that would otherwise panic at the first method call.
func (t *DispatchSprintsTool) ensureConfigured() error {
	if t == nil || t.inner == nil {
		return errors.New("dispatch_sprints: misconfigured tool (inner WriteEnrichedSprintTool is nil)")
	}
	return nil
}

func (t *DispatchSprintsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if err := t.ensureConfigured(); err != nil {
		return "", err
	}
	args, err := parseDispatchArgs(input)
	if err != nil {
		return "", err
	}
	descPath, outputDir, contract, err := t.prepareDispatch(args)
	if err != nil {
		return "", err
	}
	entries, err := readDispatchPlan(descPath)
	if err != nil {
		return "", fmt.Errorf("dispatch_sprints: %w", err)
	}

	var tot dispatchTotals
	for _, e := range entries {
		if haltErr := t.dispatchOne(ctx, contract, e, outputDir, args.Strict, &tot); haltErr != nil {
			return strings.Join(tot.perSprint, "\n"), haltErr
		}
	}
	return formatDispatchSummary(len(entries), &tot), nil
}

// parseDispatchArgs unmarshals the tool arguments; an empty input is valid
// (all fields default).
func parseDispatchArgs(input json.RawMessage) (dispatchArgs, error) {
	var args dispatchArgs
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return args, fmt.Errorf("dispatch_sprints: parse args: %w", err)
		}
	}
	return args, nil
}

// prepareDispatch applies path defaults and loads the shared contract.
func (t *DispatchSprintsTool) prepareDispatch(args dispatchArgs) (descPath, outputDir, contract string, err error) {
	if args.DescriptionsFile == "" {
		args.DescriptionsFile = ".ai/sprint_descriptions.jsonl"
	}
	descPath = args.DescriptionsFile
	if !filepath.IsAbs(descPath) && t.workDir != "" {
		descPath = filepath.Join(t.workDir, descPath)
	}
	if args.OutputDir == "" {
		args.OutputDir = ".ai/sprints"
	}
	outputDir, err = t.inner.resolveOutputDir(args.OutputDir)
	if err != nil {
		return "", "", "", fmt.Errorf("dispatch_sprints: %w", err)
	}
	contract, err = t.inner.LoadContract(args.ContractFile)
	if err != nil {
		return "", "", "", fmt.Errorf("dispatch_sprints: %w", err)
	}
	return descPath, outputDir, contract, nil
}

// dispatchOne runs one sprint, folding the outcome into tot. It returns a
// non-nil error ONLY to signal a strict-mode halt; in non-strict mode a
// per-sprint failure is recorded and nil is returned so the loop continues.
func (t *DispatchSprintsTool) dispatchOne(ctx context.Context, contract string, e dispatchEntry, outputDir string, strict bool, tot *dispatchTotals) error {
	r, runErr := t.runOneWithRetry(ctx, contract, e, outputDir)
	if runErr != nil {
		msg := fmt.Sprintf("%s: FAIL: %v", e.Path, runErr)
		tot.failures = append(tot.failures, msg)
		tot.perSprint = append(tot.perSprint, msg)
		fmt.Fprintln(os.Stderr, "dispatch_sprints: "+msg)
		if strict {
			return fmt.Errorf("dispatch_sprints: strict halt on %s: %w", e.Path, runErr)
		}
		return nil
	}
	tot.recordVerdict(r.Verdict)
	tot.totalIn += r.AuthorIn + r.AuditIn
	tot.totalOut += r.AuthorOut + r.AuditOut
	line := fmt.Sprintf(
		"%s: %d bytes, audit=%s, patches=%d, tokens=%d/%d",
		r.Path, r.Bytes, r.Verdict, r.PatchesApplied,
		r.AuthorIn+r.AuditIn, r.AuthorOut+r.AuditOut,
	)
	tot.perSprint = append(tot.perSprint, line)
	fmt.Fprintf(os.Stderr, "dispatch_sprints: %s\n", line)
	return nil
}

// formatDispatchSummary renders the aggregate header + per-sprint lines.
func formatDispatchSummary(n int, tot *dispatchTotals) string {
	status := "ok"
	if len(tot.failures) > 0 {
		status = "partial"
	}
	header := fmt.Sprintf(
		"dispatch_sprints %s: dispatched=%d (PASS=%d, PATCHED=%d, fallbacks=%d, failures=%d). Tokens: %d in / %d out.",
		status, n, tot.passes, tot.patched, tot.fallbacks, len(tot.failures), tot.totalIn, tot.totalOut,
	)
	return header + "\n" + strings.Join(tot.perSprint, "\n")
}

// dispatchMaxRetries is the upper bound on per-sprint retry attempts when the
// underlying author/audit LLM call hits a transient provider error.
const dispatchMaxRetries = 3

// runOneWithRetry wraps WriteEnrichedSprintTool.RunOne with bounded
// exponential-backoff retry for transient provider errors. Hard failures
// (auth, invalid request, content filter, etc.) do NOT retry.
func (t *DispatchSprintsTool) runOneWithRetry(ctx context.Context, contract string, e dispatchEntry, outputDir string) (*SprintRunResult, error) {
	for attempt := 1; attempt <= dispatchMaxRetries; attempt++ {
		r, err := t.inner.RunOne(ctx, contract, e.Path, e.Description, outputDir)
		if err == nil {
			logRetrySuccess(e.Path, attempt)
			return r, nil
		}
		if abortErr := t.handleAttemptFailure(ctx, e, attempt, err); abortErr != nil {
			return nil, abortErr
		}
	}
	// Unreachable: handleAttemptFailure always returns non-nil on the final
	// attempt. Present for the compiler.
	return nil, fmt.Errorf("dispatch_sprints: retry loop exited unexpectedly for %s", e.Path)
}

// logRetrySuccess notes a sprint that only succeeded after one or more retries.
func logRetrySuccess(path string, attempt int) {
	if attempt > 1 {
		fmt.Fprintf(os.Stderr, "dispatch_sprints: %s succeeded on attempt %d\n", path, attempt)
	}
}

// handleAttemptFailure decides what to do after attempt `attempt` failed with
// err. It returns a non-nil error to abort (a hard/non-retryable error, a wait
// cancellation, or the exhausted-retries error) or nil to continue looping
// after backing off.
func (t *DispatchSprintsTool) handleAttemptFailure(ctx context.Context, e dispatchEntry, attempt int, err error) error {
	if !isRetryableError(err) {
		return err
	}
	if attempt == dispatchMaxRetries {
		return fmt.Errorf("after %d attempts: %w", dispatchMaxRetries, err)
	}
	backoff := t.backoffFor(attempt)
	fmt.Fprintf(os.Stderr, "dispatch_sprints: %s attempt %d hit transient error (%v); retrying in %s\n",
		e.Path, attempt, err, backoff)
	return waitBackoff(ctx, backoff)
}

// backoffFor returns the wait duration before the next retry after `attempt`.
func (t *DispatchSprintsTool) backoffFor(attempt int) time.Duration {
	if t.retryBackoff != nil {
		return t.retryBackoff(attempt)
	}
	return time.Duration(attempt*attempt) * time.Second
}

// waitBackoff sleeps for d, returning ctx.Err() if the context is cancelled first.
func waitBackoff(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isRetryableError returns true when the error chain contains a provider
// error that the llm package marks retryable (rate limits, 5xx, timeouts,
// transient network errors). Uses errors.As so joined-error chains are
// handled correctly.
func isRetryableError(err error) bool {
	var pe llm.ProviderErrorInterface
	if errors.As(err, &pe) {
		return pe.Retryable()
	}
	return false
}
