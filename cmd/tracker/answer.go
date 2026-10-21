// ABOUTME: `tracker answer` — list runs waiting at a human gate, show a run's
// ABOUTME: gate, or answer it: validate, rebuild the run from its record, resume.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// answerFlags are `tracker answer`'s own flags and positionals.
type answerFlags struct {
	runID, label, text, answersFile string
	perGate                         []string // --answer <gate>=<answer>, repeated
	useDefault, cancel              bool
}

// hasAnswer reports whether the invocation carries an answer (else it lists
// or shows).
func (a answerFlags) hasAnswer() bool {
	return a.label != "" || a.text != "" || a.answersFile != "" || a.useDefault || a.cancel || len(a.perGate) > 0
}

// parseAnswerFlags parses `tracker answer [flags] [<run-id> [<label>]] [-- <text>]`.
// Flags may come in any order; everything after "--" is taken literally, so free
// text that starts with "-" goes there (it is the answer's text).
func parseAnswerFlags(args []string, cfg *runConfig) (runConfig, error) {
	fs := flag.NewFlagSet("answer", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a := &cfg.answer
	fs.StringVar(&cfg.workdir, "w", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.workdir, "workdir", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.artifactDir, "artifact-dir", "", "Artifact dir the run used (default: <workdir>/.tracker/runs)")
	fs.StringVar(&a.text, "text", "", "Answer with free text")
	fs.BoolVar(&a.useDefault, "default", false, "Take the gate's declared default")
	fs.StringVar(&a.answersFile, "answers-file", "", `Interview answers: JSON {"answers": {"<question id>": "<answer>"}}`)
	fs.BoolVar(&a.cancel, "cancel", false, "Cancel an interview gate")
	fs.Var((*repeatedFlag)(&a.perGate), "answer", "Answer one of several waiting gates: <gate>=<answer> (repeat once per gate)")
	fs.BoolVar(&cfg.jsonOut, "json", false, "Stream the resumed run's events as NDJSON")
	fs.BoolVar(&cfg.verbose, "verbose", false, "Verbose output for the resumed run")
	rest, literal := splitAtDashDash(args[2:])
	positional, err := parseArgsMultiPass(fs, rest)
	if err != nil {
		return *cfg, fmt.Errorf("answer: %w", err)
	}
	if err := assignAnswerPositionals(a, positional, literal); err != nil {
		return *cfg, fmt.Errorf("answer: %w", err)
	}
	return *cfg, nil
}

// splitAtDashDash splits args at the first "--".
func splitAtDashDash(args []string) (before, after []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// assignAnswerPositionals takes <run-id> [<label>] before "--" and free text
// after it (the run ID too, when it was not given before).
func assignAnswerPositionals(a *answerFlags, positional, literal []string) error {
	if len(positional) > 2 {
		return fmt.Errorf("unexpected arguments %v (free text goes after --, or in --text)", positional[2:])
	}
	padded := append(positional, "", "")
	a.runID, a.label = padded[0], padded[1]
	if a.runID == "" && len(literal) > 0 {
		a.runID, literal = literal[0], literal[1:]
	}
	return assignLiteralText(a, literal)
}

// assignLiteralText takes the words after "--" as the answer's free text.
func assignLiteralText(a *answerFlags, literal []string) error {
	if len(literal) == 0 {
		return nil
	}
	if a.text != "" || a.label != "" {
		return errors.New("give the answer once: a label, --text, or text after --")
	}
	a.text = strings.Join(literal, " ")
	return nil
}

// executeAnswer runs `tracker answer`.
func executeAnswer(cfg runConfig, deps commandDeps) error {
	runsDir := resolveArtifactDir(cfg.workdir, cfg.artifactDir)
	a := cfg.answer
	if a.runID == "" {
		return printWaitingRuns(os.Stdout, runsDir, answerDirFlags(cfg))
	}
	runID, gates, err := tracker.PendingGates(runsDir, a.runID)
	if err != nil {
		return err
	}
	if len(gates) == 0 {
		return fmt.Errorf("run %s is not waiting at a human gate", runID)
	}
	if !a.hasAnswer() {
		printPendingGates(os.Stdout, runID, gates, answerDirFlags(cfg))
		return nil
	}
	return answerRun(cfg, deps, runID, gates)
}

// answerRun validates the answer against the recorded gate, rebuilds the run
// from its invocation record, and resumes it holding the answer in memory.
func answerRun(cfg runConfig, deps commandDeps, runID string, gates []pipeline.PausedGate) error {
	in, err := answerInputFrom(cfg.answer, gates)
	if err != nil {
		return err
	}
	answers, err := tracker.ValidateAnswers(gates, in)
	if err != nil {
		return err
	}
	rec, err := readInvocation(runID)
	if err != nil {
		return err
	}
	runCfg, err := rebuildRunConfig(rec, runID, cfg)
	if err != nil {
		return err
	}
	runCfg.pendingAnswers = answers
	runCfg.expectDigest = rec.Digest
	return executeRun(runCfg, deps)
}

// rebuildRunConfig re-parses the run's recorded arguments from its recorded
// directory, resuming runID in plain (non-TUI) mode. --json / --verbose given
// to `tracker answer` apply on top.
func rebuildRunConfig(rec invocationRecord, runID string, answerCfg runConfig) (runConfig, error) {
	if rec.Cwd != "" {
		if err := os.Chdir(rec.Cwd); err != nil {
			return runConfig{}, fmt.Errorf("run %s was started in %s: %w", runID, rec.Cwd, err)
		}
		// Keep the run's own spelling of its directory (os.Getwd trusts $PWD
		// only when it names the current dir; otherwise it resolves symlinks).
		_ = os.Setenv("PWD", rec.Cwd)
	}
	cfg, err := parseFlags(append([]string{"tracker"}, rec.Args...))
	if err != nil {
		return runConfig{}, fmt.Errorf("run %s: re-reading how it was started: %w", runID, err)
	}
	cfg = applyWorkdirDefault(cfg, resolveMainWorkDir)
	cfg.resumeID = runID
	cfg.noTUI = true
	cfg.jsonOut = cfg.jsonOut || answerCfg.jsonOut
	cfg.verbose = cfg.verbose || answerCfg.verbose
	return cfg, nil
}

// answerInputFrom builds the library answer from the flags, reading
// --answers-file; each --answer <gate>=<answer> becomes one per-gate answer.
func answerInputFrom(a answerFlags, gates []pipeline.PausedGate) (tracker.AnswerInput, error) {
	in := tracker.AnswerInput{RunID: a.runID, Label: a.label, Text: a.text, Default: a.useDefault, Cancel: a.cancel}
	if len(a.perGate) > 0 {
		return perGateInput(a, in, gates)
	}
	if a.answersFile == "" {
		return in, nil
	}
	answers, err := readAnswersFile(a.answersFile)
	if err != nil {
		return in, err
	}
	in.InterviewAnswers = answers
	return in, nil
}

// perGateInput builds the answer from --answer flags, which give every gate's
// answer and so combine with no other answer flag.
func perGateInput(a answerFlags, in tracker.AnswerInput, gates []pipeline.PausedGate) (tracker.AnswerInput, error) {
	if a.label != "" || a.text != "" || a.useDefault || a.cancel || a.answersFile != "" {
		return in, errors.New("--answer gives every gate's answer; don't combine it with a label, --text, --default, --cancel or --answers-file")
	}
	per, err := perGateAnswers(a.perGate, gates)
	in.Gates = per
	return in, err
}

// perGateAnswers parses --answer <gate>=<answer> values. An answer that is not
// one of a freeform gate's labels is that gate's free text.
func perGateAnswers(values []string, gates []pipeline.PausedGate) (map[string]tracker.AnswerInput, error) {
	out := make(map[string]tracker.AnswerInput, len(values))
	for _, v := range values {
		key, answer, ok := strings.Cut(v, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--answer %q: want <gate>=<answer>", v)
		}
		out[key] = gateAnswer(key, answer, gates)
	}
	return out, nil
}

// gateAnswer reads answer as a label, or as free text for a freeform gate
// whose labels it doesn't match.
func gateAnswer(key, answer string, gates []pipeline.PausedGate) tracker.AnswerInput {
	for _, g := range gates {
		if (g.NodeID == key || pipeline.GateName(g) == key) && g.Mode == "freeform" && !hasChoiceFold(g.Choices, answer) {
			return tracker.AnswerInput{Text: answer}
		}
	}
	return tracker.AnswerInput{Label: answer}
}

func hasChoiceFold(choices []string, answer string) bool {
	for _, c := range choices {
		if strings.EqualFold(c, strings.TrimSpace(answer)) {
			return true
		}
	}
	return false
}

// repeatedFlag collects every value of a repeatable string flag.
type repeatedFlag []string

func (r *repeatedFlag) String() string     { return strings.Join(*r, ",") }
func (r *repeatedFlag) Set(v string) error { *r = append(*r, v); return nil }

// readAnswersFile reads {"answers": {"<question id>": "<answer>"}}.
func readAnswersFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--answers-file: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var doc struct {
		Answers map[string]string `json:"answers"`
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf(`--answers-file %s: want {"answers": {"<question id>": "<answer>"}}: %w`, path, err)
	}
	if doc.Answers == nil {
		return nil, fmt.Errorf(`--answers-file %s: no "answers" object`, path)
	}
	return doc.Answers, nil
}

// answerDirFlags are the -w / --artifact-dir flags the printed commands repeat.
func answerDirFlags(cfg runConfig) string {
	o := runOptions{workdir: cfg.workdir, workdirExplicit: cfg.workdirExplicit, artifactDir: cfg.artifactDir}
	return strings.Join(o.dirFlags(), " ")
}

// printWaitingRuns lists the runs in runsDir waiting at a gate.
func printWaitingRuns(w io.Writer, runsDir, flags string) error {
	runs, err := tracker.WaitingRuns(runsDir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Fprintf(w, "No runs are waiting for an answer in %s.\n", runsDir)
		return nil
	}
	fmt.Fprintf(w, "%-14s %-22s %-8s %s\n", "RUN", "GATE", "WAITING", "QUESTION")
	for _, r := range runs {
		name := pipeline.GateName(r.Gate)
		if extra := len(r.Gates) - 1; extra > 0 {
			name += fmt.Sprintf(" (+%d)", extra)
		}
		fmt.Fprintf(w, "%-14s %-22s %-8s %s\n", sanitizeLine(r.RunID), sanitizeLine(name), waitedFor(r.Since), sanitizeLine(r.Gate.Label))
	}
	fmt.Fprintf(w, "\nShow one: tracker answer %s<run-id>\n", flagsPrefix(flags))
	return nil
}

// printPendingGates shows what run runID waits for and how to answer it.
func printPendingGates(w io.Writer, runID string, gates []pipeline.PausedGate, flags string) {
	printGates(w, "⏸ Run "+runID+" is waiting at gate", gates)
	if alts := pipeline.AnswerAlternatives(gates); alts != "" {
		fmt.Fprintf(w, "other answers: %s\n", sanitizeLine(alts))
	}
	fmt.Fprintln(w, "answer with:")
	fmt.Fprintf(w, "  %s\n", pipeline.AnswerCommand(runID, flags, gates))
}

func flagsPrefix(flags string) string {
	if flags == "" {
		return ""
	}
	return flags + " "
}

// waitedFor is a short age ("3m", "2h", "4d").
func waitedFor(since time.Time) string {
	if since.IsZero() {
		return "?"
	}
	d := time.Since(since)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
