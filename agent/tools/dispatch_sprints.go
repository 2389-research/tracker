// ABOUTME: Tool that dispatches enriched-sprint generation across a JSONL plan.
// ABOUTME: Reads {path, description} per line and invokes WriteEnrichedSprintTool.RunOne for each, returning an aggregate summary.
package tools

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

var sprintPathRE = regexp.MustCompile(`^SPRINT-\d{3}\.md$`)

// readDispatchPlan parses a JSONL file into validated entries. Returns an error
// on the first malformed or invalid line so problems surface before any
// expensive LLM calls fire.
func readDispatchPlan(path string) ([]dispatchEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "dispatch_sprints: close %s: %v\n", path, cerr)
		}
	}()

	var entries []dispatchEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024) // allow long descriptions
	lineno := 0
	for sc.Scan() {
		lineno++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e dispatchEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("line %d: parse: %w", lineno, err)
		}
		if e.Path == "" {
			return nil, fmt.Errorf("line %d: missing path", lineno)
		}
		if !sprintPathRE.MatchString(e.Path) {
			return nil, fmt.Errorf("line %d: invalid path %q (want SPRINT-NNN.md)", lineno, e.Path)
		}
		if strings.TrimSpace(e.Description) == "" {
			return nil, fmt.Errorf("line %d: empty description", lineno)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no entries in %s", path)
	}
	return entries, nil
}

func (t *DispatchSprintsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	// Defensive guard: a misregistered tool (nil receiver or nil inner) would
	// otherwise panic at the first method call. Surface as a tool error so
	// the agent loop sees the failure cleanly.
	if t == nil || t.inner == nil {
		return "", errors.New("dispatch_sprints: misconfigured tool (inner WriteEnrichedSprintTool is nil)")
	}

	var args struct {
		DescriptionsFile string `json:"descriptions_file"`
		ContractFile     string `json:"contract_file"`
		OutputDir        string `json:"output_dir"`
		Strict           bool   `json:"strict"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("dispatch_sprints: parse args: %w", err)
		}
	}

	if args.DescriptionsFile == "" {
		args.DescriptionsFile = ".ai/sprint_descriptions.jsonl"
	}
	descPath := args.DescriptionsFile
	if !filepath.IsAbs(descPath) && t.workDir != "" {
		descPath = filepath.Join(t.workDir, descPath)
	}

	if args.OutputDir == "" {
		args.OutputDir = ".ai/sprints"
	}
	outputDir := t.inner.resolveOutputDir(args.OutputDir)

	contract, err := t.inner.LoadContract(args.ContractFile)
	if err != nil {
		return "", fmt.Errorf("dispatch_sprints: %w", err)
	}

	entries, err := readDispatchPlan(descPath)
	if err != nil {
		return "", fmt.Errorf("dispatch_sprints: %w", err)
	}

	var (
		perSprint   []string
		failures    []string
		passes      int
		patched     int
		fallbacks   int
		totalIn     int
		totalOut    int
	)

	for _, e := range entries {
		r, runErr := t.runOneWithRetry(ctx, contract, e, outputDir)
		if runErr != nil {
			msg := fmt.Sprintf("%s: FAIL: %v", e.Path, runErr)
			failures = append(failures, msg)
			perSprint = append(perSprint, msg)
			fmt.Fprintln(os.Stderr, "dispatch_sprints: "+msg)
			if args.Strict {
				return strings.Join(perSprint, "\n"), fmt.Errorf("dispatch_sprints: strict halt on %s: %w", e.Path, runErr)
			}
			continue
		}
		switch {
		case r.Verdict == "PASS":
			passes++
		case r.Verdict == "PATCHED" || r.Verdict == "PATCHED-PARTIAL":
			patched++
		case strings.HasPrefix(r.Verdict, "PASS-FALLBACK"):
			fallbacks++
		}
		totalIn += r.AuthorIn + r.AuditIn
		totalOut += r.AuthorOut + r.AuditOut
		perSprint = append(perSprint, fmt.Sprintf(
			"%s: %d bytes, audit=%s, patches=%d, tokens=%d/%d",
			r.Path, r.Bytes, r.Verdict, r.PatchesApplied,
			r.AuthorIn+r.AuditIn, r.AuthorOut+r.AuditOut,
		))
		fmt.Fprintf(os.Stderr, "dispatch_sprints: %s\n", perSprint[len(perSprint)-1])
	}

	status := "ok"
	if len(failures) > 0 {
		status = "partial"
	}
	header := fmt.Sprintf(
		"dispatch_sprints %s: dispatched=%d (PASS=%d, PATCHED=%d, fallbacks=%d, failures=%d). Tokens: %d in / %d out.",
		status, len(entries), passes, patched, fallbacks, len(failures), totalIn, totalOut,
	)
	return header + "\n" + strings.Join(perSprint, "\n"), nil
}

// dispatchMaxRetries is the upper bound on per-sprint retry attempts when the
// underlying author/audit LLM call hits a transient provider error.
const dispatchMaxRetries = 3

// runOneWithRetry wraps WriteEnrichedSprintTool.RunOne with bounded
// exponential-backoff retry for transient provider errors. Hard failures
// (auth, invalid request, content filter, etc.) do NOT retry.
func (t *DispatchSprintsTool) runOneWithRetry(ctx context.Context, contract string, e dispatchEntry, outputDir string) (*SprintRunResult, error) {
	var lastErr error
	for attempt := 1; attempt <= dispatchMaxRetries; attempt++ {
		r, err := t.inner.RunOne(ctx, contract, e.Path, e.Description, outputDir)
		if err == nil {
			if attempt > 1 {
				fmt.Fprintf(os.Stderr, "dispatch_sprints: %s succeeded on attempt %d\n", e.Path, attempt)
			}
			return r, nil
		}
		lastErr = err
		if !isRetryableError(err) {
			return nil, err
		}
		if attempt == dispatchMaxRetries {
			break
		}
		var backoff time.Duration
		if t.retryBackoff != nil {
			backoff = t.retryBackoff(attempt)
		} else {
			backoff = time.Duration(attempt*attempt) * time.Second
		}
		fmt.Fprintf(os.Stderr, "dispatch_sprints: %s attempt %d hit transient error (%v); retrying in %s\n",
			e.Path, attempt, err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", dispatchMaxRetries, lastErr)
}

// isRetryableError returns true when the error chain contains a provider
// error that the llm package marks retryable (rate limits, 5xx, timeouts,
// transient network errors).
func isRetryableError(err error) bool {
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		if pe, ok := cur.(llm.ProviderErrorInterface); ok {
			return pe.Retryable()
		}
	}
	return false
}
