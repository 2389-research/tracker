// ABOUTME: CLI side of a run paused at a human gate: exit code 4, the paused
// ABOUTME: summary with its answer command, and the invocation record answer reuses.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// errPausedAtGate is the run error for a paused_gate run: the process exits 4
// ("waiting for an answer"), distinct from a failure (1).
var errPausedAtGate = errors.New("paused at a human gate — answer it with tracker answer")

// exitPausedAtGate is the exit code for a run waiting at a human gate.
const exitPausedAtGate = 4

// runHints are the commands the run summary suggests: how to resume the run
// and the -w / --artifact-dir flags `tracker answer` needs.
type runHints struct {
	resume      string
	answerFlags string
}

// runHints builds the summary's resume and answer hints from the run's flags.
func (o *runOptions) runHints() runHints {
	return runHints{resume: o.resumeHintTarget(), answerFlags: strings.Join(o.dirFlags(), " ")}
}

// printGatePause prints what a paused_gate run waits for and, as the last
// line, the command that answers it.
func printGatePause(result *pipeline.EngineResult, answerFlags string) {
	fmt.Println()
	fmt.Println("─── Waiting at a human gate ───────────────────────────────")
	for _, g := range result.PausedGates {
		fmt.Printf("  ⏸ Paused at gate %s — %s\n", gateName(g), g.Label)
		if opts := gateOptionsLine(g); opts != "" {
			fmt.Printf("  %s\n", opts)
		}
		if g.Note != "" {
			fmt.Printf("  note: %s\n", g.Note)
		}
		for _, line := range strings.Split(strings.TrimRight(g.Prompt, "\n"), "\n") {
			fmt.Printf("    %s\n", line)
		}
		fmt.Println()
	}
	fmt.Println("  answer with:")
	for _, g := range result.PausedGates {
		fmt.Printf("  %s\n", answerCommand(result.RunID, answerFlags, g, len(result.PausedGates)))
	}
}

// gateOptionsLine lists what a gate accepts.
func gateOptionsLine(g pipeline.PausedGate) string {
	if g.Mode == "interview" {
		ids := make([]string, len(g.Questions))
		for i, q := range g.Questions {
			ids[i] = q.ID + ": " + q.Text
		}
		return "questions: " + strings.Join(ids, " · ")
	}
	opts := make([]string, 0, len(g.Choices)+1)
	for _, c := range g.Choices {
		if c == g.Default {
			c += " (default)"
		}
		opts = append(opts, c)
	}
	if g.Mode == "freeform" {
		opts = append(opts, "or free text")
	}
	if len(opts) == 0 {
		return ""
	}
	return "options: " + strings.Join(opts, " · ")
}

// answerCommand is the `tracker answer` command for gate g of run runID
// (--node when several gates wait).
func answerCommand(runID, flags string, g pipeline.PausedGate, total int) string {
	parts := []string{"tracker answer"}
	if flags != "" {
		parts = append(parts, flags)
	}
	parts = append(parts, runID)
	if total > 1 {
		parts = append(parts, "--node", shellQuote(gateName(g)))
	}
	return strings.Join(parts, " ") + answerSuffix(g)
}

// answerSuffix is the answer part of the command, with the alternatives as a
// shell comment.
func answerSuffix(g pipeline.PausedGate) string {
	switch {
	case g.Mode == "interview":
		return ` --answers-file answers.json   # {"answers": {"q1": "..."}}`
	case len(g.Choices) == 0:
		return ` --text "your answer"`
	}
	first := g.Default
	if first == "" {
		first = g.Choices[0]
	}
	var others []string
	for _, c := range g.Choices {
		if c != first {
			others = append(others, c)
		}
	}
	if g.Mode == "freeform" {
		others = append(others, `--text "free text"`)
	}
	if len(others) == 0 {
		return " " + shellQuote(first)
	}
	return " " + shellQuote(first) + "   # or: " + strings.Join(others, " / ")
}

// gateName is the gate's node ID prefixed by its scope path ("B1/Review").
func gateName(g pipeline.PausedGate) string {
	return strings.Join(append(append([]string(nil), g.ScopePath...), g.NodeID), "/")
}

// invocationRecord is how a run was started, kept in the secure state dir so
// `tracker answer` can rebuild the run exactly: same flags (--param values
// included), same directory, same workflow.
type invocationRecord struct {
	Version int      `json:"version"`
	Cwd     string   `json:"cwd"`
	Args    []string `json:"args"`   // the run's arguments after the program name, without -r / --from / --resume-no-rewind
	Digest  string   `json:"digest"` // pipeline.GraphDigest of the loaded workflow and subgraphs
}

// writeInvocation stores rec beside the run's secure checkpoint (0600, a
// planted symlink refused).
func writeInvocation(runID string, rec invocationRecord) error {
	path, err := pipeline.SecureInvocationPath(runID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return pipeline.WriteSecureFile(path, data)
}

// readInvocation loads the run's invocation record.
func readInvocation(runID string) (invocationRecord, error) {
	var rec invocationRecord
	path, err := pipeline.SecureInvocationPath(runID)
	if err != nil {
		return rec, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rec, fmt.Errorf("run %s has no invocation record (was it started by tracker before pause mode?): %w", runID, err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("run %s: invocation record: %w", runID, err)
	}
	return rec, nil
}

// resumeFlags are stripped from a recorded invocation; `tracker answer` adds
// its own --resume. The bool says whether the flag takes a separate value.
var resumeFlags = map[string]bool{"-r": true, "--r": true, "-resume": true, "--resume": true, "-from": true, "--from": true, "-resume-no-rewind": false, "--resume-no-rewind": false}

// stripResumeArgs removes -r/--resume, --from and --resume-no-rewind (with
// their values) from args.
func stripResumeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		takesValue, isResume := resumeFlags[name]
		if !isResume {
			out = append(out, args[i])
			continue
		}
		if takesValue && !hasValue {
			i++ // skip the separate value
		}
	}
	return out
}

// maybeWriteInvocation records how a run that paused at a gate was started.
// A failure is a warning: the run is paused either way, but `tracker answer`
// will not be able to rebuild it.
func maybeWriteInvocation(result *pipeline.EngineResult, opts *runOptions) {
	if result == nil || result.Status != pipeline.OutcomePausedGate || result.RunID == "" {
		return
	}
	rec := invocationRecord{Version: 1, Cwd: opts.cwd, Args: stripResumeArgs(opts.argv), Digest: opts.graphDigest}
	if err := writeInvocation(result.RunID, rec); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not record how run %s was started (%v); tracker answer cannot resume it\n", result.RunID, err)
	}
}
