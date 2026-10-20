// ABOUTME: CLI side of a run paused at a human gate: exit code 4, the paused
// ABOUTME: summary with its answer command, and the invocation record answer reuses.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tracker "github.com/2389-research/tracker"
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
	printGates(os.Stdout, "  ⏸ Paused at gate", result.PausedGates)
	fmt.Println("  answer with:")
	fmt.Printf("  %s\n", pipeline.AnswerCommand(result.RunID, answerFlags, result.PausedGates))
}

// printGates prints each waiting gate — name, label, options, note and full
// prompt — with control characters stripped: the prompt usually carries LLM
// output.
func printGates(w io.Writer, heading string, gates []pipeline.PausedGate) {
	for _, g := range gates {
		fmt.Fprintf(w, "%s %s — %s\n", heading, pipeline.GateName(g), sanitizeLine(g.Label))
		if opts := gateOptionsLine(g); opts != "" {
			fmt.Fprintf(w, "  %s\n", sanitizeLine(opts))
		}
		if g.Note != "" {
			fmt.Fprintf(w, "  note: %s\n", sanitizeLine(g.Note))
		}
		for _, line := range strings.Split(strings.TrimRight(sanitizeNotice(g.Prompt), "\n"), "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
		fmt.Fprintln(w)
	}
	if len(gates) > 1 {
		fmt.Fprintf(w, "  %d gates are waiting in one parallel step; answer them all in one call.\n", len(gates))
	}
}

// sanitizeLine is sanitizeNotice for one line: newlines and tabs become spaces.
func sanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, sanitizeNotice(s))
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

// checkWorkflowDigest records the loaded workflow's digest (for the invocation
// record) and, on a `tracker answer` resume, refuses a workflow that changed
// since the run paused.
func (o *runOptions) checkWorkflowDigest(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph) error {
	o.graphDigest = pipeline.GraphDigest(graph, subgraphs)
	if o.expectDigest != "" && o.graphDigest != o.expectDigest {
		return fmt.Errorf("the workflow changed since run %s paused, so the answer may not fit it any more; resume it with the new workflow by running tracker -r %s %s", o.resume.RunID, o.resume.RunID, o.resumeHintTarget())
	}
	return nil
}

// confirmAnswerTarget re-checks, now that the engine holds the run lock, that
// the gate the answer was validated against is still the one waiting.
func (o *runOptions) confirmAnswerTarget(artifactDir string) error {
	for _, pa := range o.pendingAnswers {
		if err := tracker.CheckStillWaiting(artifactDir, o.resume.RunID, pa); err != nil {
			return err
		}
	}
	return nil
}
