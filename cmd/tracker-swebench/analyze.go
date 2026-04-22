// ABOUTME: `tracker-swebench analyze <results-dir>` — bulk-triage a prior run's artifacts.
// ABOUTME: Reads predictions.jsonl, per-instance logs, and optional empty-patch diagnostics; emits a structured report.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// AnalyzeReport is the structured JSON output of `tracker-swebench analyze`.
// All sections are present in every report; sections that can't be populated
// (e.g. empty-patch diagnostics when no diagnostic files exist) expose a
// `Notes` field explaining the degraded mode.
type AnalyzeReport struct {
	ResultsDir           string                 `json:"results_dir"`
	RunMeta              *RunMeta               `json:"run_meta,omitempty"`
	EvaluatorReportPath  string                 `json:"evaluator_report_path,omitempty"`
	Counts               AnalyzeCounts          `json:"counts"`
	PerRepo              []RepoBreakdown        `json:"per_repo"`
	TopEmptyPatches      EmptyPatchSection      `json:"top_empty_patches"`
	TopLongestUnresolved []UnresolvedInstance   `json:"top_longest_unresolved"`
	ErrorClasses         ErrorClassDistribution `json:"error_classes"`
}

// AnalyzeCounts is the overall counts section with percentages.
// `ResolvedKnown` is true iff an evaluator report was located and parsed.
// When false, Resolved is 0 and Unresolved aggregates all non-empty non-error
// instances under "patched but unverified".
type AnalyzeCounts struct {
	Total                int     `json:"total"`
	Resolved             int     `json:"resolved"`
	Unresolved           int     `json:"unresolved"`
	PatchedUnverified    int     `json:"patched_unverified"`
	Empty                int     `json:"empty"`
	Errors               int     `json:"errors"`
	ResolvedPct          float64 `json:"resolved_pct"`
	UnresolvedPct        float64 `json:"unresolved_pct"`
	PatchedUnverifiedPct float64 `json:"patched_unverified_pct"`
	EmptyPct             float64 `json:"empty_pct"`
	ErrorsPct            float64 `json:"errors_pct"`
	ResolvedKnown        bool    `json:"resolved_known"`
}

// RepoBreakdown is one row of the per-repo table. `ResolvedKnown` mirrors the
// counts section: when false, Resolved is 0 and Rate is computed as
// (non-empty-non-error)/total — a patch-produced rate, not a resolve rate.
type RepoBreakdown struct {
	Repo              string  `json:"repo"`
	Total             int     `json:"total"`
	Resolved          int     `json:"resolved"`
	Unresolved        int     `json:"unresolved"`
	PatchedUnverified int     `json:"patched_unverified"`
	Empty             int     `json:"empty"`
	Errors            int     `json:"errors"`
	Rate              float64 `json:"rate"`
	ResolvedKnown     bool    `json:"resolved_known"`
}

// EmptyPatchSection contains the top-10 empty-patch rankings. If no
// `<id>.empty-patch.json` files were found, Instances is empty and Notes
// explains the degraded mode.
type EmptyPatchSection struct {
	DiagnosticsAvailable bool              `json:"diagnostics_available"`
	Notes                string            `json:"notes,omitempty"`
	TotalEmpty           int               `json:"total_empty"`
	DiagnosticsFound     int               `json:"diagnostics_found"`
	Instances            []EmptyPatchEntry `json:"instances"`
}

// EmptyPatchEntry is one empty-patch row. FinalMessage is capped at ~200 chars
// for the human-readable table; the corresponding diagnostic written to disk
// may also contain a truncated message (WriteEmptyPatchDiagnostic trims
// FinalMessage to 400 runes before persisting).
type EmptyPatchEntry struct {
	InstanceID        string   `json:"instance_id"`
	Turns             int      `json:"turns"`
	TerminationReason string   `json:"termination_reason"`
	FinalMessage      string   `json:"final_message"`
	LastToolCalls     []string `json:"last_tool_calls"`
}

// UnresolvedInstance is one row of the "longest unresolved" table. Elapsed
// strings are kept as-is from the log (e.g. "2m3s") rather than parsed to
// duration — they're only used for display and sort tiebreak.
type UnresolvedInstance struct {
	InstanceID   string `json:"instance_id"`
	Turns        int    `json:"turns"`
	Elapsed      string `json:"elapsed"`
	ElapsedSecs  int64  `json:"elapsed_secs"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// ErrorClassDistribution reports the split from #140. Populated by scanning
// per-instance log files for `error:` lines and classifying via the same
// heuristic the runtime uses.
type ErrorClassDistribution struct {
	Total   int                `json:"total"`
	Setup   int                `json:"setup"`
	Patch   int                `json:"patch"`
	Harness int                `json:"harness"`
	Samples []ErrorClassSample `json:"samples"`
}

// ErrorClassSample is a representative error drawn from one instance in each
// class (up to 3 per class). Used to make the human report more actionable.
type ErrorClassSample struct {
	Class      string `json:"class"`
	InstanceID string `json:"instance_id"`
	Message    string `json:"message"`
}

// perInstanceData is the internal per-instance record built from on-disk
// artifacts. One per known instance_id. Entries are only created by
// readPredictions — scanLogsDir only updates existing entries, so every
// record here is backed by a predictions.jsonl line.
type perInstanceData struct {
	InstanceID   string
	Repo         string
	PatchEmpty   bool
	Turns        int
	Elapsed      string
	ElapsedSecs  int64
	InputTokens  int64
	OutputTokens int64
	ErrorMsg     string
	ErrorClass   string // "", "setup", "patch", "harness"
	Diagnostic   *EmptyPatchDiagnostic
}

// runAnalyze is the `analyze` subcommand entry point. args is the flag args
// *after* the "analyze" verb.
func runAnalyze(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(out)
	emitJSON := fs.Bool("json", false, "emit structured JSON report instead of human-readable text")
	topN := fs.Int("top", 10, "number of rows in top-N sections (empty-patch, longest-unresolved)")
	predictionsPath := fs.String("predictions", "", "explicit path to predictions.jsonl (overrides auto-discovery)")

	fs.Usage = func() {
		fmt.Fprintf(out, `tracker-swebench analyze — bulk triage of a completed run

Usage:
  tracker-swebench analyze <results-dir> [flags]

Reads artifacts produced by a prior tracker-swebench run (predictions.jsonl,
logs/*.log, logs/*.empty-patch.json) and emits a structured triage report.

predictions.jsonl is auto-discovered: first at <results-dir>/predictions.jsonl,
then at the sibling path <results-dir>/../predictions.jsonl (matches the default
output layout). Pass --predictions <path> to point at an arbitrary location.

By default, resolution (resolved vs unresolved) requires an evaluator report
file in the results dir: either a SWE-bench harness JSON report containing a
"resolved_ids" array, or a file named resolved_ids.json / resolution.json.
When absent, the report falls back to "patched but unverified" classification.

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("analyze: missing <results-dir> argument")
	}
	resultsDir := fs.Arg(0)

	report, err := BuildAnalyzeReportWithPredictions(resultsDir, *topN, *predictionsPath)
	if err != nil {
		return err
	}

	if *emitJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return WriteAnalyzeText(out, report)
}

// BuildAnalyzeReport is the pure analysis function: given a results dir,
// produces the AnalyzeReport. Separated from runAnalyze for testability.
// Equivalent to BuildAnalyzeReportWithPredictions(resultsDir, topN, "").
func BuildAnalyzeReport(resultsDir string, topN int) (*AnalyzeReport, error) {
	return BuildAnalyzeReportWithPredictions(resultsDir, topN, "")
}

// BuildAnalyzeReportWithPredictions is the same as BuildAnalyzeReport but
// allows the caller to override predictions.jsonl auto-discovery. When
// predictionsOverride is empty, the function looks at <results-dir>/predictions.jsonl
// first, then falls back to <results-dir>/../predictions.jsonl (the default
// `tracker-swebench run` layout writes predictions as a sibling of the results
// directory).
func BuildAnalyzeReportWithPredictions(resultsDir string, topN int, predictionsOverride string) (*AnalyzeReport, error) {
	if topN <= 0 {
		topN = 10
	}
	absDir, err := filepath.Abs(resultsDir)
	if err != nil {
		return nil, fmt.Errorf("resolve results dir: %w", err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("stat results dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", absDir)
	}

	report := &AnalyzeReport{ResultsDir: absDir}

	// Optional run metadata.
	meta, err := readRunMeta(filepath.Join(absDir, "run_meta.json"))
	if err != nil {
		return nil, fmt.Errorf("read run_meta.json: %w", err)
	}
	report.RunMeta = meta

	// Per-instance dataset, keyed by instance_id.
	data := make(map[string]*perInstanceData)

	// 1. Read predictions.jsonl — source of truth for which instances
	//    the harness actually attempted. Discovery order:
	//      a. --predictions <path> override, if set
	//      b. <results-dir>/predictions.jsonl
	//      c. <results-dir>/../predictions.jsonl (default run layout writes
	//         --output=./predictions.jsonl as a sibling of --results-dir=./results)
	predPath, err := resolvePredictionsPath(absDir, predictionsOverride)
	if err != nil {
		return nil, err
	}
	if err := readPredictions(predPath, data); err != nil {
		return nil, err
	}

	// 2. Scan logs/ to fill in turns, elapsed, tokens, errors,
	//    and empty-patch diagnostics.
	logsDir := filepath.Join(absDir, "logs")
	if _, err := os.Stat(logsDir); err == nil {
		if err := scanLogsDir(logsDir, data); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat logs dir: %w", err)
	}

	// 3. Optional evaluator report — populates resolved_ids if present.
	resolvedSet, evalReportPath, err := findEvaluatorReport(absDir)
	if err != nil {
		return nil, err
	}
	report.EvaluatorReportPath = evalReportPath
	resolvedKnown := resolvedSet != nil

	// 4. Compute counts.
	report.Counts = computeCounts(data, resolvedSet)

	// 5. Per-repo breakdown.
	report.PerRepo = computePerRepo(data, resolvedSet)

	// 6. Top empty-patch instances.
	report.TopEmptyPatches = computeTopEmpty(data, topN)

	// 7. Top longest unresolved instances.
	report.TopLongestUnresolved = computeTopLongestUnresolved(data, resolvedSet, resolvedKnown, topN)

	// 8. Error class distribution.
	report.ErrorClasses = computeErrorClassDistribution(data)

	return report, nil
}

// readRunMeta loads run_meta.json into a RunMeta. Missing file is a non-error;
// returns (nil, err) only on read/parse failure of an existing file.
func readRunMeta(path string) (*RunMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse run_meta.json: %w", err)
	}
	return &meta, nil
}

// resolvePredictionsPath returns the path to predictions.jsonl to read.
// Resolution order mirrors the documented --predictions flag:
//  1. explicit override, if non-empty — used verbatim; existence is checked so
//     we fail with a clear message instead of deep inside readPredictions.
//  2. <absDir>/predictions.jsonl — the "everything inside results-dir" layout.
//  3. <absDir>/../predictions.jsonl — the default `tracker-swebench run` layout
//     where --output defaults to ./predictions.jsonl (cwd) and --results-dir
//     defaults to ./results. The sibling path matches that default.
//
// Returns an error with both candidate paths listed when neither exists, so the
// user can see why discovery failed.
func resolvePredictionsPath(absDir, override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("--predictions %q: file not found", override)
			}
			return "", fmt.Errorf("--predictions %q: %w", override, err)
		}
		return override, nil
	}
	primary := filepath.Join(absDir, "predictions.jsonl")
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %q: %w", primary, err)
	}
	sibling := filepath.Join(filepath.Dir(absDir), "predictions.jsonl")
	if _, err := os.Stat(sibling); err == nil {
		return sibling, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %q: %w", sibling, err)
	}
	return "", fmt.Errorf("predictions.jsonl not found — looked in %q and %q (pass --predictions <path> to override)", primary, sibling)
}

// readPredictions populates data from predictions.jsonl. Each JSONL line is
// one Prediction. An empty model_patch marks the instance as empty-patch.
func readPredictions(path string, data map[string]*perInstanceData) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open predictions.jsonl %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	const maxBuf = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, maxBuf), maxBuf)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var p Prediction
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			// Skip unparseable lines rather than aborting — a mixed or
			// partially-corrupted file is still useful to triage.
			continue
		}
		if p.InstanceID == "" {
			continue
		}
		d, ok := data[p.InstanceID]
		if !ok {
			d = &perInstanceData{InstanceID: p.InstanceID, Repo: repoFromInstanceID(p.InstanceID)}
			data[p.InstanceID] = d
		}
		d.PatchEmpty = p.ModelPatch == ""
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan predictions.jsonl: %w", err)
	}
	return nil
}

// repoFromInstanceID extracts the "owner/name" repo from a SWE-bench instance
// ID: `django__django-11099` → `django/django`. Falls back to "unknown" for
// IDs that don't contain the "__" separator.
func repoFromInstanceID(id string) string {
	// The SWE-bench convention encodes "owner__name-<pr>" in the instance ID,
	// where "__" is the owner/name separator. The final segment is the PR/issue
	// number preceded by a hyphen.
	idx := strings.Index(id, "__")
	if idx < 0 {
		return "unknown"
	}
	owner := id[:idx]
	rest := id[idx+2:]
	// Trim the trailing "-<digits>" (e.g. "django-11099" → "django").
	dash := strings.LastIndex(rest, "-")
	if dash >= 0 {
		name := rest[:dash]
		if name != "" {
			return owner + "/" + name
		}
	}
	return owner + "/" + rest
}

// scanLogsDir walks the logs/ directory and updates per-instance data with
// turns, elapsed, tokens, errors (from `<id>.log`) and empty-patch diagnostics
// (from `<id>.empty-patch.json`). `<id>.transcript.log` is ignored — analyze
// does not re-read transcripts to stay fast.
//
// Only instances already present in `data` (from predictions.jsonl) are
// updated. Orphan log files from a prior partial run without a matching
// prediction line are skipped so counts/tables reflect exactly the set of
// instances the harness actually recorded. This matches the Copilot review
// guidance on `HasPrediction`: predictions.jsonl is the canonical instance
// set; logs/ augments it.
func scanLogsDir(logsDir string, data map[string]*perInstanceData) error {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return fmt.Errorf("read logs dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(logsDir, name)
		switch {
		case strings.HasSuffix(name, ".empty-patch.json"):
			id := strings.TrimSuffix(name, ".empty-patch.json")
			d, ok := data[id]
			if !ok {
				continue // orphan diagnostic without a prediction line; skip
			}
			diag, err := readEmptyPatchDiagnostic(path)
			if err == nil {
				d.Diagnostic = diag
				if d.Turns == 0 && diag.Turns > 0 {
					d.Turns = diag.Turns
				}
			}
		case strings.HasSuffix(name, ".transcript.log"):
			// Skip — we don't parse transcripts in analyze.
			continue
		case strings.HasSuffix(name, ".log"):
			id := strings.TrimSuffix(name, ".log")
			d, ok := data[id]
			if !ok {
				continue // orphan log without a prediction line; skip
			}
			if err := parseInstanceLog(path, d); err != nil {
				// Per-file parse failures are warnings, not fatal — other
				// files may still be readable.
				continue
			}
		}
	}
	return nil
}

// readEmptyPatchDiagnostic unmarshals a `<id>.empty-patch.json` file produced
// by WriteEmptyPatchDiagnostic.
func readEmptyPatchDiagnostic(path string) (*EmptyPatchDiagnostic, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d EmptyPatchDiagnostic
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parse empty-patch diagnostic %q: %w", path, err)
	}
	return &d, nil
}

// parseInstanceLog extracts known `key: value` pairs from a tracker-swebench
// per-instance log file. The format is fixed-width plain text written by the
// harness, not structured JSON. Unknown keys are ignored. Errors in one line
// don't stop the parse — we collect what we can.
func parseInstanceLog(path string, d *perInstanceData) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Error lines may carry a multi-line stderr dump. Give the scanner a
	// generous buffer so we don't truncate the first line.
	const maxBuf = 1 * 1024 * 1024
	sc.Buffer(make([]byte, maxBuf), maxBuf)

	// The "error:" field is intentionally collected across the rest of the
	// file — harness writes it last and doesn't cap its length. But for
	// classification we only need the first ~8KB.
	var errorBuf strings.Builder
	inErrorSection := false

	for sc.Scan() {
		line := sc.Text()
		if inErrorSection {
			if errorBuf.Len() < classifyErrorScanLimit {
				errorBuf.WriteString(line)
				errorBuf.WriteByte('\n')
			}
			continue
		}
		k, v := splitKV(line)
		switch k {
		case "instance_id":
			if d.InstanceID == "" {
				d.InstanceID = v
			}
		case "turns":
			if n, err := strconv.Atoi(v); err == nil {
				d.Turns = n
			}
		case "elapsed":
			d.Elapsed = v
			d.ElapsedSecs = parseElapsedSecs(v)
		case "input_tokens":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				d.InputTokens = n
			}
		case "output_tokens":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				d.OutputTokens = n
			}
		case "error":
			inErrorSection = true
			errorBuf.WriteString(v)
			errorBuf.WriteByte('\n')
		case "write_prediction_error":
			// Treat write-prediction errors as harness errors too.
			if errorBuf.Len() == 0 {
				errorBuf.WriteString(v)
			}
			d.ErrorClass = "harness"
		}
	}
	if errorBuf.Len() > 0 {
		msg := strings.TrimSpace(errorBuf.String())
		d.ErrorMsg = msg
		if d.ErrorClass == "" {
			d.ErrorClass = classNameFor(classifyRunError(fmt.Errorf("%s", msg)))
		}
	}
	return sc.Err()
}

// splitKV parses a "key: value" line. Returns ("", "") for lines without a
// colon so the caller can skip them.
func splitKV(line string) (string, string) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", ""
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
}

// elapsedRE matches Go duration-style `<n>m<n>s` or `<n>s` as written by
// time.Duration.String(). Only matches what the harness actually writes.
var elapsedRE = regexp.MustCompile(`^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$`)

// parseElapsedSecs converts a Go-style duration string like "2m3s" or "45s"
// to total seconds. Returns 0 for unparseable input.
func parseElapsedSecs(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	m := elapsedRE.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	var total int64
	if m[1] != "" {
		h, _ := strconv.ParseInt(m[1], 10, 64)
		total += h * 3600
	}
	if m[2] != "" {
		mins, _ := strconv.ParseInt(m[2], 10, 64)
		total += mins * 60
	}
	if m[3] != "" {
		sec, _ := strconv.ParseInt(m[3], 10, 64)
		total += sec
	}
	return total
}

// classNameFor maps the internal runErrorClass enum to a stable string used
// in the report. Keep these in sync with the handler in results.go.
func classNameFor(c runErrorClass) string {
	switch c {
	case runErrorSetup:
		return "setup"
	case runErrorPatch:
		return "patch"
	default:
		return "harness"
	}
}

// findEvaluatorReport scans resultsDir for a file that looks like an SWE-bench
// evaluator report and extracts its `resolved_ids`. Returns (nil, "", nil) if
// no such file is present — that's the expected degraded mode. The report
// file conventions we understand:
//   - any top-level JSON object with a "resolved_ids" array
//   - file names: *.evaluation.json, *.report.json, resolved_ids.json, resolution.json
func findEvaluatorReport(resultsDir string) (map[string]struct{}, string, error) {
	candidates := []string{}
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return nil, "", fmt.Errorf("read results dir %q: %w", resultsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		switch {
		case name == "resolved_ids.json",
			name == "resolution.json",
			name == "report.json",
			strings.HasSuffix(name, ".evaluation.json"),
			strings.HasSuffix(name, ".report.json"):
			candidates = append(candidates, filepath.Join(resultsDir, name))
		}
	}
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Accept any JSON object with a "resolved_ids" array, or a bare array.
		var asObj map[string]any
		if err := json.Unmarshal(raw, &asObj); err == nil {
			if ids, ok := extractResolvedIDs(asObj); ok {
				return ids, path, nil
			}
		}
		// Bare-array form. A nil asList means the JSON was not an array at all;
		// an empty-but-non-nil asList is a valid evaluator report with zero
		// resolved instances (ResolvedKnown=true, Resolved=0), which must be
		// distinguished from "no evaluator report".
		var asList []string
		if err := json.Unmarshal(raw, &asList); err == nil && asList != nil {
			return stringSet(asList), path, nil
		}
	}
	return nil, "", nil
}

// extractResolvedIDs pulls a resolved-IDs set out of a generic decoded JSON
// object. Accepts the SWE-bench canonical `resolved_ids: [...]` array. If the
// field is missing or not a list of strings, returns (nil, false).
func extractResolvedIDs(obj map[string]any) (map[string]struct{}, bool) {
	v, ok := obj["resolved_ids"]
	if !ok {
		return nil, false
	}
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]struct{}, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out[s] = struct{}{}
		}
	}
	return out, true
}

// stringSet converts a slice of strings to a set.
func stringSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		if s != "" {
			out[s] = struct{}{}
		}
	}
	return out
}

// computeCounts tallies the overall section.
func computeCounts(data map[string]*perInstanceData, resolvedSet map[string]struct{}) AnalyzeCounts {
	c := AnalyzeCounts{ResolvedKnown: resolvedSet != nil}
	for _, d := range data {
		c.Total++
		switch {
		case d.ErrorMsg != "":
			c.Errors++
		case d.PatchEmpty:
			c.Empty++
		case resolvedSet != nil:
			if _, ok := resolvedSet[d.InstanceID]; ok {
				c.Resolved++
			} else {
				c.Unresolved++
			}
		default:
			c.PatchedUnverified++
		}
	}
	if c.Total > 0 {
		t := float64(c.Total)
		c.ResolvedPct = pct(c.Resolved, t)
		c.UnresolvedPct = pct(c.Unresolved, t)
		c.PatchedUnverifiedPct = pct(c.PatchedUnverified, t)
		c.EmptyPct = pct(c.Empty, t)
		c.ErrorsPct = pct(c.Errors, t)
	}
	return c
}

// pct returns n/total as a percent. total is a float to spare the caller a cast.
func pct(n int, total float64) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / total * 100
}

// computePerRepo groups data by repo and produces a sorted breakdown.
// Sort order: resolve rate descending (known) or patch rate descending
// (unknown), tiebroken by total instances descending then repo name.
func computePerRepo(data map[string]*perInstanceData, resolvedSet map[string]struct{}) []RepoBreakdown {
	byRepo := make(map[string]*RepoBreakdown)
	resolvedKnown := resolvedSet != nil
	for _, d := range data {
		repo := d.Repo
		if repo == "" {
			repo = "unknown"
		}
		r, ok := byRepo[repo]
		if !ok {
			r = &RepoBreakdown{Repo: repo, ResolvedKnown: resolvedKnown}
			byRepo[repo] = r
		}
		r.Total++
		switch {
		case d.ErrorMsg != "":
			r.Errors++
		case d.PatchEmpty:
			r.Empty++
		case resolvedKnown:
			if _, ok := resolvedSet[d.InstanceID]; ok {
				r.Resolved++
			} else {
				r.Unresolved++
			}
		default:
			r.PatchedUnverified++
		}
	}
	out := make([]RepoBreakdown, 0, len(byRepo))
	for _, r := range byRepo {
		if r.Total > 0 {
			if resolvedKnown {
				r.Rate = pct(r.Resolved, float64(r.Total))
			} else {
				nonEmptyNonError := r.Total - r.Empty - r.Errors
				r.Rate = pct(nonEmptyNonError, float64(r.Total))
			}
		}
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rate != out[j].Rate {
			return out[i].Rate > out[j].Rate
		}
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// computeTopEmpty produces the top-N empty-patch entries. Ranking: prefer
// instances with diagnostics (we can surface more info), then highest turns,
// then instance_id alphabetical for deterministic output.
func computeTopEmpty(data map[string]*perInstanceData, topN int) EmptyPatchSection {
	var empties []*perInstanceData
	diagCount := 0
	for _, d := range data {
		if !d.PatchEmpty || d.ErrorMsg != "" {
			continue
		}
		empties = append(empties, d)
		if d.Diagnostic != nil {
			diagCount++
		}
	}
	sec := EmptyPatchSection{
		DiagnosticsAvailable: diagCount > 0,
		TotalEmpty:           len(empties),
		DiagnosticsFound:     diagCount,
	}
	if len(empties) > 0 && diagCount == 0 {
		sec.Notes = "no diagnostic data — run with newer tracker-swebench (>= PR #150) to enable per-instance empty-patch diagnostics"
	}
	sort.SliceStable(empties, func(i, j int) bool {
		hi := empties[i].Diagnostic != nil
		hj := empties[j].Diagnostic != nil
		if hi != hj {
			return hi
		}
		if empties[i].Turns != empties[j].Turns {
			return empties[i].Turns > empties[j].Turns
		}
		return empties[i].InstanceID < empties[j].InstanceID
	})
	n := topN
	if n > len(empties) {
		n = len(empties)
	}
	sec.Instances = make([]EmptyPatchEntry, 0, n)
	for _, d := range empties[:n] {
		entry := EmptyPatchEntry{
			InstanceID: d.InstanceID,
			Turns:      d.Turns,
		}
		if d.Diagnostic != nil {
			entry.TerminationReason = d.Diagnostic.TerminationReason
			entry.FinalMessage = trimFinalMessage(d.Diagnostic.FinalMessage, 200)
			entry.LastToolCalls = d.Diagnostic.LastToolCalls
			if entry.Turns == 0 {
				entry.Turns = d.Diagnostic.Turns
			}
		}
		sec.Instances = append(sec.Instances, entry)
	}
	return sec
}

// trimFinalMessage collapses internal whitespace runs and truncates to max
// runes, appending an ellipsis marker when it cuts. Used only for the
// human-readable table.
func trimFinalMessage(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// computeTopLongestUnresolved produces the top-N longest unresolved instances.
// "Longest" is lex-sorted by (turns desc, elapsed_secs desc, instance_id asc).
// Errors and empty-patch instances are excluded — those have their own sections.
func computeTopLongestUnresolved(data map[string]*perInstanceData, resolvedSet map[string]struct{}, resolvedKnown bool, topN int) []UnresolvedInstance {
	var candidates []*perInstanceData
	for _, d := range data {
		if d.ErrorMsg != "" || d.PatchEmpty {
			continue
		}
		if resolvedKnown {
			if _, ok := resolvedSet[d.InstanceID]; ok {
				continue // resolved, skip
			}
		}
		candidates = append(candidates, d)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Turns != candidates[j].Turns {
			return candidates[i].Turns > candidates[j].Turns
		}
		if candidates[i].ElapsedSecs != candidates[j].ElapsedSecs {
			return candidates[i].ElapsedSecs > candidates[j].ElapsedSecs
		}
		return candidates[i].InstanceID < candidates[j].InstanceID
	})
	n := topN
	if n > len(candidates) {
		n = len(candidates)
	}
	out := make([]UnresolvedInstance, 0, n)
	for _, d := range candidates[:n] {
		out = append(out, UnresolvedInstance{
			InstanceID:   d.InstanceID,
			Turns:        d.Turns,
			Elapsed:      d.Elapsed,
			ElapsedSecs:  d.ElapsedSecs,
			InputTokens:  d.InputTokens,
			OutputTokens: d.OutputTokens,
		})
	}
	return out
}

// computeErrorClassDistribution tallies the setup/patch/harness split and
// surfaces up to 3 representative samples per class.
func computeErrorClassDistribution(data map[string]*perInstanceData) ErrorClassDistribution {
	var dist ErrorClassDistribution
	samplesByClass := map[string][]ErrorClassSample{
		"setup":   nil,
		"patch":   nil,
		"harness": nil,
	}
	// Iterate in deterministic instance-id order for stable sample selection.
	ids := make([]string, 0, len(data))
	for id := range data {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := data[id]
		if d.ErrorMsg == "" {
			continue
		}
		dist.Total++
		switch d.ErrorClass {
		case "setup":
			dist.Setup++
		case "patch":
			dist.Patch++
		default:
			dist.Harness++
		}
		class := d.ErrorClass
		if class == "" {
			class = "harness"
		}
		if len(samplesByClass[class]) < 3 {
			samplesByClass[class] = append(samplesByClass[class], ErrorClassSample{
				Class:      class,
				InstanceID: d.InstanceID,
				Message:    trimFinalMessage(d.ErrorMsg, 200),
			})
		}
	}
	// Order samples: setup, patch, harness.
	for _, class := range []string{"setup", "patch", "harness"} {
		dist.Samples = append(dist.Samples, samplesByClass[class]...)
	}
	return dist
}

// errWriter wraps an io.Writer and remembers the first error encountered.
// Subsequent writes after an error are no-ops so callers can freely stream
// output and only check the error once at the end. Used by WriteAnalyzeText
// so broken-pipe / short-write failures surface instead of being silently
// dropped by `fmt.Fprint*`'s unchecked return value.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}

func (e *errWriter) println(args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintln(e.w, args...)
}

// WriteAnalyzeText formats the report as the default human-readable output.
// Uses plain ASCII tables — no external dependencies, safe for non-TTY pipes.
func WriteAnalyzeText(w io.Writer, r *AnalyzeReport) error {
	ew := &errWriter{w: w}
	ew.println("tracker-swebench analyze")
	ew.println("========================")
	ew.printf("Results dir: %s\n", r.ResultsDir)
	if r.RunMeta != nil {
		m := r.RunMeta
		ew.printf("Model:       %s  (provider: %s", m.Model, m.Provider)
		if m.GatewayURL != "" {
			ew.printf(", gateway: %s", m.GatewayURL)
		}
		ew.println(")")
		ew.printf("Max turns:   %d    Timeout: %s\n", m.MaxTurns, m.Timeout)
		if m.Commit != "" {
			ew.printf("Commit:      %s\n", m.Commit)
		}
	}
	if r.EvaluatorReportPath != "" {
		ew.printf("Evaluator:   %s\n", r.EvaluatorReportPath)
	}
	ew.println()

	writeOverallCounts(ew, r.Counts)
	writePerRepo(ew, r.PerRepo)
	writeTopEmpty(ew, r.TopEmptyPatches)
	writeTopLongest(ew, r.TopLongestUnresolved, r.Counts.ResolvedKnown)
	writeErrorClasses(ew, r.ErrorClasses)
	return ew.err
}

func writeOverallCounts(w *errWriter, c AnalyzeCounts) {
	w.println("## Overall counts")
	w.println()
	if c.Total == 0 {
		w.println("  (no instances found)")
		w.println()
		return
	}
	w.printf("  Total:              %d\n", c.Total)
	if c.ResolvedKnown {
		w.printf("  Resolved:           %d (%.1f%%)\n", c.Resolved, c.ResolvedPct)
		w.printf("  Unresolved:         %d (%.1f%%)\n", c.Unresolved, c.UnresolvedPct)
	} else {
		w.printf("  Patched (unverified): %d (%.1f%%)\n", c.PatchedUnverified, c.PatchedUnverifiedPct)
		w.println("  (no evaluator report — resolution status unknown)")
	}
	w.printf("  Empty:              %d (%.1f%%)\n", c.Empty, c.EmptyPct)
	w.printf("  Errors:             %d (%.1f%%)\n", c.Errors, c.ErrorsPct)
	w.println()
}

func writePerRepo(w *errWriter, rows []RepoBreakdown) {
	w.println("## Per-repo breakdown")
	w.println()
	if len(rows) == 0 {
		w.println("  (no repos)")
		w.println()
		return
	}
	resolvedKnown := rows[0].ResolvedKnown
	// Compute max repo-name width.
	repoW := len("Repo")
	for _, r := range rows {
		if len(r.Repo) > repoW {
			repoW = len(r.Repo)
		}
	}
	if resolvedKnown {
		w.printf("  %-*s  %5s  %8s  %10s  %5s  %6s  %5s\n",
			repoW, "Repo", "Total", "Resolved", "Unresolved", "Empty", "Errors", "Rate")
		w.printf("  %s\n", strings.Repeat("-", repoW+4+5+2+8+2+10+2+5+2+6+2+5))
		for _, r := range rows {
			w.printf("  %-*s  %5d  %8d  %10d  %5d  %6d  %4.1f%%\n",
				repoW, r.Repo, r.Total, r.Resolved, r.Unresolved, r.Empty, r.Errors, r.Rate)
		}
	} else {
		w.printf("  %-*s  %5s  %9s  %5s  %6s  %10s\n",
			repoW, "Repo", "Total", "Patched", "Empty", "Errors", "Patch rate")
		w.printf("  %s\n", strings.Repeat("-", repoW+4+5+2+9+2+5+2+6+2+10))
		for _, r := range rows {
			w.printf("  %-*s  %5d  %9d  %5d  %6d  %9.1f%%\n",
				repoW, r.Repo, r.Total, r.PatchedUnverified, r.Empty, r.Errors, r.Rate)
		}
	}
	w.println()
}

func writeTopEmpty(w *errWriter, sec EmptyPatchSection) {
	w.printf("## Top empty-patch instances (%d total)\n\n", sec.TotalEmpty)
	if sec.TotalEmpty == 0 {
		w.println("  (no empty-patch instances)")
		w.println()
		return
	}
	if !sec.DiagnosticsAvailable {
		w.println("  no diagnostic data — run with newer tracker-swebench (>= PR #150)")
		w.println("  to enable per-instance empty-patch diagnostics.")
		w.println()
		w.println("  Instance IDs (top-N by turns):")
		for _, e := range sec.Instances {
			w.printf("    - %s (turns: %d)\n", e.InstanceID, e.Turns)
		}
		w.println()
		return
	}
	w.printf("  diagnostics found for %d/%d empty-patch instances.\n\n", sec.DiagnosticsFound, sec.TotalEmpty)
	for i, e := range sec.Instances {
		w.printf("  %d. %s\n", i+1, e.InstanceID)
		w.printf("     turns: %d   termination: %s\n", e.Turns, displayOrDash(e.TerminationReason))
		if len(e.LastToolCalls) > 0 {
			w.printf("     last tools: %s\n", strings.Join(e.LastToolCalls, " → "))
		}
		if e.FinalMessage != "" {
			w.printf("     final: %s\n", e.FinalMessage)
		}
	}
	w.println()
}

func writeTopLongest(w *errWriter, rows []UnresolvedInstance, resolvedKnown bool) {
	title := "## Top longest unresolved instances"
	if !resolvedKnown {
		title = "## Top longest non-resolved-or-empty instances"
	}
	w.println(title)
	w.println()
	if len(rows) == 0 {
		w.println("  (no candidates)")
		w.println()
		return
	}
	idW := len("Instance")
	for _, r := range rows {
		if len(r.InstanceID) > idW {
			idW = len(r.InstanceID)
		}
	}
	w.printf("  %-*s  %5s  %9s  %12s  %12s\n",
		idW, "Instance", "Turns", "Elapsed", "Input tokens", "Output tokens")
	w.printf("  %s\n", strings.Repeat("-", idW+2+5+2+9+2+12+2+12))
	for _, r := range rows {
		elapsed := r.Elapsed
		if elapsed == "" {
			elapsed = "-"
		}
		w.printf("  %-*s  %5d  %9s  %12s  %12s\n",
			idW, r.InstanceID, r.Turns, elapsed,
			formatInt64(r.InputTokens), formatInt64(r.OutputTokens))
	}
	w.println()
}

// formatInt64 returns "-" for zero, otherwise the decimal string. Makes the
// table easier to scan — most rows have tokens populated.
func formatInt64(n int64) string {
	if n == 0 {
		return "-"
	}
	return strconv.FormatInt(n, 10)
}

// displayOrDash returns "-" for an empty string, otherwise the input.
func displayOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeErrorClasses(w *errWriter, d ErrorClassDistribution) {
	w.println("## Error class distribution")
	w.println()
	if d.Total == 0 {
		w.println("  (no errors)")
		w.println()
		return
	}
	total := float64(d.Total)
	w.printf("  Total errors:  %d\n", d.Total)
	w.printf("  Setup:         %d (%.1f%%)\n", d.Setup, pct(d.Setup, total))
	w.printf("  Patch:         %d (%.1f%%)\n", d.Patch, pct(d.Patch, total))
	w.printf("  Harness:       %d (%.1f%%)\n", d.Harness, pct(d.Harness, total))
	if len(d.Samples) > 0 {
		w.println()
		w.println("  Samples (up to 3 per class):")
		for _, s := range d.Samples {
			w.printf("    [%s] %s\n         %s\n", s.Class, s.InstanceID, s.Message)
		}
	}
	w.println()
}
