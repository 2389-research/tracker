// ABOUTME: Pause-mode library seam: read the gate a run waits at, validate an
// ABOUTME: answer against it, resume the run with it (Answer), list waiting runs.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// GateModePause makes human gates pause the run (Config.GateMode); GateModeConsole
// answers them from stdin.
const (
	GateModePause   = "pause"
	GateModeConsole = "console"
)

// PausedGateInfo names the human gate a paused_gate run waits at.
type PausedGateInfo struct {
	Node  string   `json:"node"`
	Scope []string `json:"scope,omitempty"`
	Label string   `json:"label,omitempty"`
	Mode  string   `json:"mode,omitempty"`
}

// pausedGateInfo summarizes the first waiting gate, or nil when none waits.
func pausedGateInfo(gates []pipeline.PausedGate) *PausedGateInfo {
	if len(gates) == 0 {
		return nil
	}
	g := gates[0]
	return &PausedGateInfo{Node: g.NodeID, Scope: g.ScopePath, Label: g.Label, Mode: g.Mode}
}

// AnswerInput is an answer to a paused gate. Exactly one of Label, Text,
// Default, InterviewAnswers and Cancel is set.
type AnswerInput struct {
	RunID string
	// Node picks the gate when several wait (parallel branches): a node ID, or
	// "<scope>/<node>" with the scope path joined by "/".
	Node string
	// Label is a choice (choice / yes_no / labeled freeform gates).
	Label string
	// Text is free text (freeform gates).
	Text string
	// Default takes the gate's declared default.
	Default bool
	// InterviewAnswers maps each interview question ID (GateQuestion.ID) to
	// its answer.
	InterviewAnswers map[string]string
	// Cancel cancels an interview gate (the gate fails, routable).
	Cancel bool
}

// ValidateAnswer checks in against the paused gate g and returns the answer
// bound to it (node, scope, fingerprint, nonce). Labels match case-insensitively
// after trimming and come back in the gate's own spelling; an error lists the
// valid answers.
func ValidateAnswer(g pipeline.PausedGate, in AnswerInput) (handlers.PendingAnswer, error) {
	pa := handlers.PendingAnswer{NodeID: g.NodeID, ScopePath: g.ScopePath, Fingerprint: g.Fingerprint, Nonce: g.Nonce}
	if n := answerKinds(in); n != 1 {
		return pa, fmt.Errorf("give exactly one answer (a label, --text, --default, --answers-file or --cancel); got %d", n)
	}
	var err error
	switch g.Mode {
	case "interview":
		pa.Interview, pa.Cancel, err = validateInterview(g, in)
	case "freeform":
		pa.Label, pa.Text, err = validateFreeform(g, in)
	default: // choice, yes_no
		pa.Label, err = validateChoice(g, in)
	}
	return pa, err
}

func answerKinds(in AnswerInput) int {
	n := 0
	for _, set := range []bool{in.Label != "", in.Text != "", in.Default, in.InterviewAnswers != nil, in.Cancel} {
		if set {
			n++
		}
	}
	return n
}

func validateChoice(g pipeline.PausedGate, in AnswerInput) (string, error) {
	switch {
	case in.Default:
		return defaultOf(g)
	case in.Label != "":
		return matchChoice(g.Choices, in.Label)
	case in.Cancel:
		return "", errors.New("--cancel applies only to interview gates")
	default:
		return "", fmt.Errorf("this gate takes one of: %s (not --text or an answers file)", strings.Join(g.Choices, ", "))
	}
}

func validateFreeform(g pipeline.PausedGate, in AnswerInput) (label, text string, err error) {
	switch {
	case in.Text != "":
		return "", in.Text, nil
	case in.Default:
		label, err = defaultOf(g)
		return label, "", err
	case in.Label != "" && len(g.Choices) > 0:
		label, err = matchChoice(g.Choices, in.Label)
		if err != nil {
			err = fmt.Errorf("%w; for free text use --text", err)
		}
		return label, "", err
	case in.Cancel:
		return "", "", errors.New("--cancel applies only to interview gates")
	default:
		return "", "", errors.New("this gate takes free text: use --text")
	}
}

func validateInterview(g pipeline.PausedGate, in AnswerInput) (map[string]string, bool, error) {
	if in.Cancel {
		return nil, true, nil
	}
	if in.InterviewAnswers == nil {
		return nil, false, errors.New("this interview gate takes --answers-file (or --cancel)")
	}
	out := make(map[string]string, len(g.Questions))
	for _, q := range g.Questions {
		v, err := answerQuestion(q, in.InterviewAnswers[q.ID])
		if err != nil {
			return nil, false, err
		}
		out[q.ID] = v
	}
	for id := range in.InterviewAnswers {
		if _, ok := out[id]; !ok {
			return nil, false, fmt.Errorf("unknown question %s (the gate asks %s)", id, questionIDs(g.Questions))
		}
	}
	return out, false, nil
}

// answerQuestion validates one interview answer: required, and one of the
// question's options when it has any.
func answerQuestion(q pipeline.GateQuestion, v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("question %s (%q) has no answer", q.ID, q.Text)
	}
	if len(q.Options) == 0 {
		return v, nil
	}
	m, err := matchChoice(q.Options, v)
	if err != nil {
		return "", fmt.Errorf("question %s: %w", q.ID, err)
	}
	return m, nil
}

func questionIDs(qs []pipeline.GateQuestion) string {
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	return strings.Join(ids, ", ")
}

func defaultOf(g pipeline.PausedGate) (string, error) {
	if g.Default == "" {
		return "", errors.New("this gate has no default; give an answer")
	}
	return g.Default, nil
}

// matchChoice returns the choice equal to answer after trimming and case-folding.
func matchChoice(choices []string, answer string) (string, error) {
	want := strings.TrimSpace(answer)
	for _, c := range choices {
		if strings.EqualFold(c, want) {
			return c, nil
		}
	}
	return "", fmt.Errorf("%q is not an answer to this gate; choose one of: %s", want, strings.Join(choices, ", "))
}

// PendingGates returns the gates run runID (an ID or unique prefix in
// artifactDir) waits at, from its authoritative checkpoint — never from the
// run dir's gate.json mirror — and the resolved run ID.
func PendingGates(artifactDir, runID string) (string, []pipeline.PausedGate, error) {
	cpPath, err := resolveCheckpointOrSecure(artifactDir, runID)
	if err != nil {
		return "", nil, err
	}
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		return "", nil, err
	}
	return cp.RunID, cp.PausedGates, nil
}

// pickGate selects the gate an answer is for: the only one, or the one node
// names (a node ID or "<scope>/<node>").
func pickGate(gates []pipeline.PausedGate, node string) (pipeline.PausedGate, error) {
	if node == "" {
		if len(gates) == 1 {
			return gates[0], nil
		}
		return pipeline.PausedGate{}, fmt.Errorf("%d gates are waiting (%s); pick one with --node", len(gates), gateNames(gates))
	}
	for _, g := range gates {
		if g.NodeID == node || gateName(g) == node {
			return g, nil
		}
	}
	return pipeline.PausedGate{}, fmt.Errorf("no gate %q is waiting (waiting: %s)", node, gateNames(gates))
}

func gateName(g pipeline.PausedGate) string {
	return strings.Join(append(slices.Clone(g.ScopePath), g.NodeID), "/")
}

func gateNames(gates []pipeline.PausedGate) string {
	names := make([]string, len(gates))
	for i, g := range gates {
		names[i] = g.NodeID
	}
	return strings.Join(names, ", ")
}

// checkStillWaiting re-reads the checkpoint (under the run lock) and refuses
// when the gate the answer was validated against has been replaced.
func checkStillWaiting(artifactDir, runID string, pa handlers.PendingAnswer) error {
	_, gates, err := PendingGates(artifactDir, runID)
	if err != nil {
		return err
	}
	for _, g := range gates {
		if g.NodeID == pa.NodeID && slices.Equal(g.ScopePath, pa.ScopePath) && g.Nonce == pa.Nonce {
			return nil
		}
	}
	return fmt.Errorf("run %s changed while you were answering — look at it again", runID)
}

// Answer resumes paused run in.RunID with an answer to the gate it waits at.
// source and cfg are the run's own (the caller rebuilds what it started the
// run with); cfg.ResumeRunID and cfg.Interviewer are set here. The answer is
// held in memory by this process only. The run lock is held for the whole
// resume, so a concurrent answer or resume of the same run is refused.
func Answer(ctx context.Context, source string, cfg Config, in AnswerInput) (*Result, error) {
	workDir, err := resolveWorkDir(cfg.WorkingDir)
	if err != nil {
		return nil, err
	}
	runsDir := resumeRunsDir(cfg, workDir)
	runID, gates, err := PendingGates(runsDir, in.RunID)
	if err != nil {
		return nil, err
	}
	if len(gates) == 0 {
		return nil, fmt.Errorf("run %s is not waiting at a gate (status %s)", runID, runStatusOf(runsDir, runID))
	}
	g, err := pickGate(gates, in.Node)
	if err != nil {
		return nil, err
	}
	pa, err := ValidateAnswer(g, in)
	if err != nil {
		return nil, err
	}
	cfg.ResumeRunID = runID
	cfg.Interviewer = handlers.NewPausingInterviewer(&pa)
	eng, err := NewEngineWithContext(ctx, source, cfg)
	if err != nil {
		return nil, err
	}
	defer eng.Close()
	if err := checkStillWaiting(runsDir, runID, pa); err != nil {
		return nil, err
	}
	return eng.Run(ctx)
}

// runStatusOf is the run's audit status, or "unknown".
func runStatusOf(runsDir, runID string) string {
	rs, ok := buildRunSummary(runsDir, runID, ioDiscard{})
	if !ok {
		return "unknown"
	}
	return rs.Status
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

// WaitingRun is a run waiting at a human gate.
type WaitingRun struct {
	RunID string
	// Gate is the first waiting gate (lowest branch index); Gates all of them.
	Gate  pipeline.PausedGate
	Gates []pipeline.PausedGate
	Since time.Time
}

// WaitingRuns lists the runs in artifactDir that wait at a human gate, newest
// first, reading each run's authoritative checkpoint.
func WaitingRuns(artifactDir string) ([]WaitingRun, error) {
	entries, err := os.ReadDir(artifactDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []WaitingRun
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runID, gates, gerr := PendingGates(artifactDir, e.Name())
		if gerr != nil || len(gates) == 0 {
			continue
		}
		out = append(out, WaitingRun{RunID: runID, Gate: gates[0], Gates: gates, Since: gates[0].At})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.After(out[j].Since) })
	return out, nil
}
