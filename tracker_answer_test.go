// ABOUTME: Tests the pause-mode library seam: GateMode, ValidateAnswer, Answer,
// ABOUTME: WaitingRuns, and that only the authoritative checkpoint record counts.
package tracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

const answerDip = `workflow answertest
  start: Start
  exit: Done

  agent Start
    label: Start

  human Gate
    label: "Ship it?"
    default: reject

  tool Ship
    command: "true"

  tool Drop
    command: "true"

  agent Done
    label: Done

  edges
    Start -> Gate
    Gate -> Ship  label: "approve"
    Gate -> Drop  label: "reject"
    Ship -> Done
    Drop -> Done
`

func pauseConfig(t *testing.T) Config {
	t.Helper()
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	dir := t.TempDir()
	return Config{Format: "dip", WorkingDir: dir, ArtifactDir: filepath.Join(dir, "runs"), GateMode: "pause"}
}

func runPaused(t *testing.T, cfg Config) *Result {
	t.Helper()
	res, _ := Run(context.Background(), answerDip, cfg)
	if res == nil || res.Status != string(pipeline.OutcomePausedGate) {
		t.Fatalf("run = %+v, want paused_gate", res)
	}
	return res
}

func TestValidateAnswer_Table(t *testing.T) {
	choice := pipeline.PausedGate{NodeID: "G", Mode: "choice", Choices: []string{"approve", "reject"}, Default: "reject", Fingerprint: "f", Nonce: "n"}
	noDefault := choice
	noDefault.Default = ""
	labeled := pipeline.PausedGate{NodeID: "G", Mode: "freeform", Choices: []string{"revise", "approve"}, Default: "approve"}
	plain := pipeline.PausedGate{NodeID: "G", Mode: "freeform"}
	yesNo := pipeline.PausedGate{NodeID: "G", Mode: "yes_no", Choices: []string{"Yes", "No"}}
	interview := pipeline.PausedGate{NodeID: "G", Mode: "interview", Questions: []pipeline.GateQuestion{
		{ID: "q1", Text: "DB?", Options: []string{"pg", "sqlite"}}, {ID: "q2", Text: "Name?"},
	}}

	ok := []struct {
		name string
		g    pipeline.PausedGate
		in   AnswerInput
		want handlers.PendingAnswer
	}{
		{"choice label (RF3)", choice, AnswerInput{Label: "  Approve "}, handlers.PendingAnswer{Label: "approve"}},
		{"choice default", choice, AnswerInput{Default: true}, handlers.PendingAnswer{Label: "reject"}},
		{"labeled label", labeled, AnswerInput{Label: "REVISE"}, handlers.PendingAnswer{Label: "revise"}},
		{"labeled text", labeled, AnswerInput{Text: "shorter title"}, handlers.PendingAnswer{Text: "shorter title"}},
		{"labeled default", labeled, AnswerInput{Default: true}, handlers.PendingAnswer{Label: "approve"}},
		{"plain text", plain, AnswerInput{Text: "anything"}, handlers.PendingAnswer{Text: "anything"}},
		{"yes_no", yesNo, AnswerInput{Label: "yes"}, handlers.PendingAnswer{Label: "Yes"}},
		{"interview", interview, AnswerInput{InterviewAnswers: map[string]string{"q1": "PG", "q2": "orders"}}, handlers.PendingAnswer{Interview: map[string]string{"q1": "pg", "q2": "orders"}}},
		{"interview cancel", interview, AnswerInput{Cancel: true}, handlers.PendingAnswer{Cancel: true}},
	}
	for _, tc := range ok {
		got, err := ValidateAnswer(tc.g, tc.in)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
			continue
		}
		if got.Label != tc.want.Label || got.Text != tc.want.Text || got.Cancel != tc.want.Cancel || len(got.Interview) != len(tc.want.Interview) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
		for k, v := range tc.want.Interview {
			if got.Interview[k] != v {
				t.Errorf("%s: interview[%s] = %q, want %q", tc.name, k, got.Interview[k], v)
			}
		}
		if got.NodeID != tc.g.NodeID || got.Fingerprint != tc.g.Fingerprint || got.Nonce != tc.g.Nonce {
			t.Errorf("%s: answer not bound to the gate: %+v", tc.name, got)
		}
	}

	bad := []struct {
		name, wantErr string
		g             pipeline.PausedGate
		in            AnswerInput
	}{
		{"unknown label lists choices", "approve, reject", choice, AnswerInput{Label: "aprove"}},
		{"choice has no default", "no default", noDefault, AnswerInput{Default: true}},
		{"choice rejects text", "--text", choice, AnswerInput{Text: "hi"}},
		{"labeled typo", "revise, approve", labeled, AnswerInput{Label: "revize"}},
		{"plain needs text", "free text", plain, AnswerInput{Label: "x"}},
		{"plain no default", "no default", plain, AnswerInput{Default: true}},
		{"yes_no no default", "no default", yesNo, AnswerInput{Default: true}},
		{"interview missing", "q2", interview, AnswerInput{InterviewAnswers: map[string]string{"q1": "pg"}}},
		{"interview bad option", "pg, sqlite", interview, AnswerInput{InterviewAnswers: map[string]string{"q1": "mysql", "q2": "x"}}},
		{"interview unknown key", "q9", interview, AnswerInput{InterviewAnswers: map[string]string{"q1": "pg", "q2": "x", "q9": "y"}}},
		{"two answers", "exactly one", choice, AnswerInput{Label: "approve", Default: true}},
		{"no answer", "exactly one", choice, AnswerInput{}},
		{"cancel outside interview", "--cancel", choice, AnswerInput{Cancel: true}},
	}
	for _, tc := range bad {
		_, err := ValidateAnswer(tc.g, tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestResolveInterviewer_GateModePrecedence(t *testing.T) {
	iv, err := resolveInterviewer(Config{AutoApprove: true, GateMode: "pause", ArtifactDir: "x"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, isAuto := iv.(*handlers.AutoApproveFreeformInterviewer); !isAuto {
		t.Errorf("AutoApprove + GateMode pause resolved to %T, want auto-approve", iv)
	}
	iv, err = resolveInterviewer(Config{GateMode: "pause", ArtifactDir: "x"}, nil, nil)
	if _, isPause := iv.(*handlers.PausingInterviewer); err != nil || !isPause {
		t.Errorf("GateMode pause = %T, %v", iv, err)
	}
	iv, err = resolveInterviewer(Config{GateMode: "console"}, nil, nil)
	if _, isConsole := iv.(*handlers.ConsoleInterviewer); err != nil || !isConsole {
		t.Errorf("GateMode console = %T, %v", iv, err)
	}
	if _, err := resolveInterviewer(Config{GateMode: "bogus"}, nil, nil); err == nil {
		t.Error("GateMode bogus accepted")
	}
	if _, err := resolveInterviewer(Config{GateMode: "pause"}, nil, nil); err == nil || !strings.Contains(err.Error(), "ArtifactDir") {
		t.Errorf("GateMode pause without ArtifactDir: err = %v", err)
	}
}

func TestAnswer_RoundTrip(t *testing.T) {
	cfg := pauseConfig(t)
	res := runPaused(t, cfg)
	if res.PausedGate == nil || res.PausedGate.Node != "Gate" || res.PausedGate.Label != "Ship it?" {
		t.Fatalf("Result.PausedGate = %+v", res.PausedGate)
	}
	cfg.GateMode = ""
	got, err := Answer(context.Background(), answerDip, cfg, AnswerInput{RunID: res.RunID, Label: "approve"})
	if err != nil || got.Status != string(pipeline.OutcomeSuccess) {
		t.Fatalf("Answer = %+v, %v", got, err)
	}
	if !slices.Contains(got.CompletedNodes, "Ship") || slices.Contains(got.CompletedNodes, "Drop") {
		t.Errorf("completed = %v, want the approve edge", got.CompletedNodes)
	}
}

func TestAnswer_RefusesNotWaiting(t *testing.T) {
	cfg := pauseConfig(t)
	res := runPaused(t, cfg)
	if _, err := Answer(context.Background(), answerDip, cfg, AnswerInput{RunID: res.RunID, Label: "approve"}); err != nil {
		t.Fatal(err)
	}
	_, err := Answer(context.Background(), answerDip, cfg, AnswerInput{RunID: res.RunID, Label: "approve"})
	if err == nil || !strings.Contains(err.Error(), "not waiting") {
		t.Errorf("answer to a finished run: err = %v", err)
	}
}

func TestValidateAnswers_EveryGateAnswered(t *testing.T) {
	two := []pipeline.PausedGate{
		{NodeID: "A", Mode: "freeform"},
		{NodeID: "G", ScopePath: []string{"B"}, Mode: "choice", Choices: []string{"go"}},
	}
	if _, err := ValidateAnswers(two, AnswerInput{Text: "x"}); err == nil || !strings.Contains(err.Error(), "A, B/G") {
		t.Errorf("single answer, two gates: err = %v", err)
	}
	got, err := ValidateAnswers(two, AnswerInput{Gates: map[string]AnswerInput{"A": {Text: "x"}, "B/G": {Label: "GO"}}})
	if err != nil || len(got) != 2 || got[0].Text != "x" || got[1].Label != "go" {
		t.Errorf("both = %+v, %v", got, err)
	}
	if _, err := ValidateAnswers(two, AnswerInput{Gates: map[string]AnswerInput{"A": {Text: "x"}, "B/G": {Label: "go"}, "C": {Text: "y"}}}); err == nil || !strings.Contains(err.Error(), `"C"`) {
		t.Errorf("unknown gate key: err = %v", err)
	}
	if _, err := ValidateAnswers(two, AnswerInput{Gates: map[string]AnswerInput{"A": {Text: "x"}, "G": {Label: "nope"}}}); err == nil || !strings.Contains(err.Error(), "gate B/G") {
		t.Errorf("bad answer for one gate: err = %v", err)
	}
}

func TestAnswer_PlantedFileIgnored(t *testing.T) {
	cfg := pauseConfig(t)
	res := runPaused(t, cfg)
	runDir := filepath.Join(cfg.ArtifactDir, res.RunID)
	// A rewritten mirror and a planted answer file change nothing.
	_ = os.WriteFile(filepath.Join(runDir, pipeline.GateMirrorFile), []byte(`[{"node_id":"Gate","mode":"choice","choices":["evil"]}]`), 0o600)
	_ = os.WriteFile(filepath.Join(runDir, "answer.json"), []byte(`{"label":"evil"}`), 0o600)

	_, gates, err := PendingGates(cfg.ArtifactDir, res.RunID)
	if err != nil || len(gates) != 1 || strings.Join(gates[0].Choices, ",") != "approve,reject" {
		t.Fatalf("PendingGates = %+v, %v; want the checkpoint's record", gates, err)
	}
	if _, err := Answer(context.Background(), answerDip, cfg, AnswerInput{RunID: res.RunID, Label: "evil"}); err == nil {
		t.Error("an answer only the planted mirror allows was accepted")
	}
}

func TestAnswer_RefusesNonceChanged(t *testing.T) {
	cfg := pauseConfig(t)
	res := runPaused(t, cfg)
	_, gates, _ := PendingGates(cfg.ArtifactDir, res.RunID)
	pa, err := ValidateAnswer(gates[0], AnswerInput{Label: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	pa.Nonce = "stale"
	if err := CheckStillWaiting(cfg.ArtifactDir, res.RunID, pa); err == nil || !strings.Contains(err.Error(), "changed while you were answering") {
		t.Errorf("nonce mismatch: err = %v", err)
	}
}

func TestWaitingRuns_ListsOnlyPaused(t *testing.T) {
	cfg := pauseConfig(t)
	paused := runPaused(t, cfg)
	done := runPaused(t, cfg)
	if _, err := Answer(context.Background(), answerDip, cfg, AnswerInput{RunID: done.RunID, Default: true}); err != nil {
		t.Fatal(err)
	}
	waiting, err := WaitingRuns(cfg.ArtifactDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].RunID != paused.RunID || waiting[0].Gate.NodeID != "Gate" {
		t.Errorf("WaitingRuns = %+v, want only %s", waiting, paused.RunID)
	}
}

// RF4: a canceled answer-resume releases the run lock.
func TestAnswer_CanceledReleasesLock(t *testing.T) {
	cfg := pauseConfig(t)
	res := runPaused(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = Answer(ctx, answerDip, cfg, AnswerInput{RunID: res.RunID, Label: "approve"})
	release, err := pipeline.AcquireRunLock(res.RunID)
	if err != nil {
		t.Fatalf("lock still held after a canceled answer: %v", err)
	}
	release()
}

func TestResumeLock_RefusesConcurrentResume(t *testing.T) {
	cfg := pauseConfig(t)
	res := runPaused(t, cfg)
	release, err := pipeline.AcquireRunLock(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cfg.ResumeRunID = res.RunID
	_, err = NewEngine(answerDip, cfg)
	if !errors.Is(err, pipeline.ErrRunLocked) {
		t.Errorf("resume while locked: err = %v, want ErrRunLocked", err)
	}
}

// A resume by explicit checkpoint path (the CLI's -r passes the resolved
// CheckpointDir) takes the lock too.
func TestResumeLock_ExplicitCheckpointPathLocks(t *testing.T) {
	cfg := pauseConfig(t)
	res := runPaused(t, cfg)
	cpPath, err := ResolveCheckpointIn(cfg.ArtifactDir, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	release, err := pipeline.AcquireRunLock(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cfg.CheckpointDir = cpPath
	_, err = NewEngine(answerDip, cfg)
	if !errors.Is(err, pipeline.ErrRunLocked) {
		t.Errorf("resume by checkpoint path while locked: err = %v, want ErrRunLocked", err)
	}
}

const twoGatesDip = `workflow twogates
  start: Start
  exit: Done

  agent Start
    label: Start

  parallel P -> G1, G2

  human G1
    label: "First?"
    mode: freeform

  human G2
    label: "Second?"
    mode: freeform

  fan_in J <- G1, G2

  agent Done
    label: Done

  edges
    Start -> P
    J -> Done
`

// Two branch gates in one parallel node are answered together; answering
// them one at a time would re-run the node and re-pause the other forever.
func TestAnswer_TwoBranchGatesBothAnswered(t *testing.T) {
	cfg := pauseConfig(t)
	res, _ := Run(context.Background(), twoGatesDip, cfg)
	if res == nil || res.Status != string(pipeline.OutcomePausedGate) {
		t.Fatalf("run = %+v, want paused_gate", res)
	}
	_, gates, err := PendingGates(cfg.ArtifactDir, res.RunID)
	if err != nil || len(gates) != 2 {
		t.Fatalf("PendingGates = %+v, %v; want two", gates, err)
	}
	cfg.GateMode = ""
	if _, err := Answer(context.Background(), twoGatesDip, cfg, AnswerInput{RunID: res.RunID, Text: "only one"}); err == nil || !strings.Contains(err.Error(), "2 gates are waiting") {
		t.Fatalf("a single answer with two gates waiting: err = %v", err)
	}
	if _, err := Answer(context.Background(), twoGatesDip, cfg, AnswerInput{RunID: res.RunID, Gates: map[string]AnswerInput{"G1": {Text: "a"}}}); err == nil || !strings.Contains(err.Error(), "G2") {
		t.Fatalf("answering one of two: err = %v, want it to name G2", err)
	}
	got, err := Answer(context.Background(), twoGatesDip, cfg, AnswerInput{RunID: res.RunID, Gates: map[string]AnswerInput{"G1": {Text: "a"}, "G2": {Text: "b"}}})
	if err != nil || got.Status != string(pipeline.OutcomeSuccess) {
		t.Fatalf("answering both = %+v, %v; want success", got, err)
	}
}

// When one of several answered branch gates asks a changed question, the
// re-pause records every gate branch of the parallel node again — the node
// re-runs, so the gate whose answer was used needs one again too. Recording
// only the re-asked gate made the run alternate between gates forever.
func TestAnswer_BranchGateRepauseKeepsSiblings(t *testing.T) {
	cfg := pauseConfig(t)
	res, _ := Run(context.Background(), twoGatesDip, cfg)
	_, gates, err := PendingGates(cfg.ArtifactDir, res.RunID)
	if err != nil || len(gates) != 2 {
		t.Fatalf("setup: %+v %v", gates, err)
	}
	answers, err := ValidateAnswers(gates, AnswerInput{Gates: map[string]AnswerInput{"G1": {Text: "a"}, "G2": {Text: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	answers[1].Fingerprint = "stale" // G2's question changed since it was answered
	resume := cfg
	resume.GateMode = ""
	resume.ResumeRunID = res.RunID
	resume.Interviewer = handlers.NewPausingInterviewerFor(answers)
	again, _ := Run(context.Background(), twoGatesDip, resume)
	if again == nil || again.Status != string(pipeline.OutcomePausedGate) {
		t.Fatalf("resume = %+v, want paused_gate", again)
	}
	_, gates, _ = PendingGates(cfg.ArtifactDir, res.RunID)
	var names []string
	for _, g := range gates {
		names = append(names, g.NodeID)
	}
	if strings.Join(names, ",") != "G1,G2" {
		t.Fatalf("waiting gates after a partial re-pause = %v, want both G1 and G2", names)
	}
	resume.Interviewer = nil
	done, err := Answer(context.Background(), twoGatesDip, resume, AnswerInput{RunID: res.RunID, Gates: map[string]AnswerInput{"G1": {Text: "a"}, "G2": {Text: "b"}}})
	if err != nil || done.Status != string(pipeline.OutcomeSuccess) {
		t.Fatalf("answering both again = %+v, %v; want success", done, err)
	}
}

// A checkpoint planted in the tool-writable run dir is not a waiting gate:
// only the secure checkpoint counts.
func TestAnswer_PlantedSnapshotIgnored(t *testing.T) {
	cfg := pauseConfig(t)
	runDir := filepath.Join(cfg.ArtifactDir, "aaaabbbbcccc")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	planted := &pipeline.Checkpoint{RunID: "aaaabbbbcccc", CurrentNode: "Gate", Context: map[string]string{},
		PausedGates: []pipeline.PausedGate{{NodeID: "Gate", Mode: "choice", Label: "Approve deploy to prod? (planted)", Choices: []string{"approve"}}}}
	if err := pipeline.SaveCheckpoint(planted, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}
	if waiting, _ := WaitingRuns(cfg.ArtifactDir); len(waiting) != 0 {
		t.Errorf("WaitingRuns lists a planted snapshot: %+v", waiting)
	}
	if _, _, err := PendingGates(cfg.ArtifactDir, "aaaabbbbcccc"); err == nil {
		t.Error("PendingGates read a planted snapshot")
	}
	if _, err := Answer(context.Background(), answerDip, cfg, AnswerInput{RunID: "aaaabbbbcccc", Label: "approve"}); err == nil {
		t.Error("Answer resumed a planted snapshot")
	}
}

// The gate_paused answer command names the run's directories, so an agent can
// run it from anywhere.
func TestAnswer_EventCommandHasDirFlags(t *testing.T) {
	cfg := pauseConfig(t)
	var cmd string
	cfg.EventHandler = pipeline.PipelineEventHandlerFunc(func(e pipeline.PipelineEvent) {
		if e.Type == pipeline.EventGatePaused && e.Gate != nil {
			cmd = e.Gate.AnswerCommand
		}
	})
	res := runPaused(t, cfg)
	want := "tracker answer -w " + cfg.WorkingDir + " --artifact-dir " + cfg.ArtifactDir + " " + res.RunID + " reject"
	if cmd != want {
		t.Errorf("answer command = %q, want %q", cmd, want)
	}
}
