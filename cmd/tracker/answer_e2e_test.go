// ABOUTME: End to end with the built binary: quickfix pauses at its gates without a
// ABOUTME: terminal and `tracker answer` drives it to a commit; digest, JSON and TTY cases.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// exitCode is the process exit code of a finished tracker run (0 on success).
func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("tracker did not run: %v", err)
	}
	return ee.ExitCode()
}

// pausedRunID returns the ID of the one run in the repo with a gate.json mirror.
func (e *firstWinEnv) pausedRunID(t *testing.T) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(e.repo, ".tracker", "runs", "*", "gate.json"))
	if len(matches) != 1 {
		t.Fatalf("want one paused run with gate.json, got %v", matches)
	}
	return filepath.Base(filepath.Dir(matches[0]))
}

// logClaudePrompts makes the fake claude append each prompt it gets to a log.
func (e *firstWinEnv) logClaudePrompts(t *testing.T) string {
	t.Helper()
	logPath := filepath.Join(filepath.Dir(e.bin), "claude-prompts.log")
	script := strings.Replace(fakeClaudeScript, "#!/bin/sh\n",
		"#!/bin/sh\nprintf '%s\\n---\\n' \"$*\" >> '"+logPath+"'\n", 1)
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.bin), "fakebin", "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return logPath
}

func TestAnswerE2E_Quickfix(t *testing.T) {
	e := newFirstWinEnv(t)
	prompts := e.logClaudePrompts(t)
	head := e.git(t, "rev-parse", "HEAD")

	out, err := e.tracker(t, "quickfix", "--param", "change=fix the README typo")
	if code := exitCode(t, err); code != 4 {
		t.Fatalf("quickfix without a terminal: exit %d, want 4 (paused)\n%s", code, out)
	}
	id := e.pausedRunID(t)
	if !strings.Contains(out, "⏸ Paused at gate ReviewDiff") || !strings.Contains(out, "tracker answer "+id+" reject") {
		t.Fatalf("pause summary missing the gate or the answer command:\n%s", out)
	}

	out, err = e.tracker(t, "answer", id, "revise")
	if code := exitCode(t, err); code != 4 || !strings.Contains(out, "Paused at gate AskRevision") {
		t.Fatalf("answer revise: exit %d, want 4 at AskRevision\n%s", code, out)
	}
	out, err = e.tracker(t, "answer", id, "--text", "also add a newline")
	if code := exitCode(t, err); code != 4 || !strings.Contains(out, "Paused at gate ReviewDiff") {
		t.Fatalf("answer --text: exit %d, want 4 back at ReviewDiff\n%s", code, out)
	}
	out, err = e.tracker(t, "answer", id, "approve")
	if code := exitCode(t, err); code != 0 {
		t.Fatalf("answer approve: exit %d\n%s", code, out)
	}

	if br := e.git(t, "branch", "--list", "tracker/quickfix/"+id); br == "" {
		t.Errorf("approve must commit to tracker/quickfix/%s\n%s", id, out)
	}
	if got := e.git(t, "rev-parse", "HEAD"); got != head {
		t.Errorf("the user's HEAD moved: %s → %s", head, got)
	}
	if b, _ := os.ReadFile(filepath.Join(e.repo, "README.md")); string(b) != "base\n" {
		t.Errorf("the user's README changed: %q", b)
	}
	log, _ := os.ReadFile(prompts)
	if n := strings.Count(string(log), "fix the README typo"); n < 2 {
		t.Errorf("--param change reached %d MakeChange prompts, want both (first run and the revision):\n%s", n, log)
	}
	if !strings.Contains(string(log), "also add a newline") {
		t.Errorf("the revision text never reached MakeChange:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(e.repo, ".tracker", "runs", id, "gate.json")); !os.IsNotExist(err) {
		t.Errorf("gate.json must be gone once the run finished (stat err %v)", err)
	}
}

func TestAnswerE2E_DefaultRejects(t *testing.T) {
	e := newFirstWinEnv(t)
	out, err := e.tracker(t, "quickfix", "--param", "change=fix the README")
	if code := exitCode(t, err); code != 4 {
		t.Fatalf("exit %d, want 4\n%s", code, out)
	}
	id := e.pausedRunID(t)
	out, err = e.tracker(t, "answer", id, "--default")
	if code := exitCode(t, err); code != 0 {
		t.Fatalf("answer --default: exit %d\n%s", code, out)
	}
	if br := e.git(t, "branch", "--list", "tracker/quickfix/*"); br != "" {
		t.Errorf("--default (reject) must remove the branch, got %q", br)
	}
}

// The bad answers are refused before anything resumes.
func TestAnswerE2E_Refusals(t *testing.T) {
	e := newFirstWinEnv(t)
	if _, err := e.tracker(t, "quickfix", "--param", "change=fix the README"); exitCode(t, err) != 4 {
		t.Fatal("setup did not pause")
	}
	id := e.pausedRunID(t)
	for args, want := range map[string]string{
		"aprove":             "choose one of: reject, revise, approve",
		"--answers-file x":   "--answers-file",
		"approve --node Nop": "no gate",
	} {
		out, err := e.tracker(t, append([]string{"answer", id}, strings.Fields(args)...)...)
		if exitCode(t, err) != 1 || !strings.Contains(out, want) {
			t.Errorf("answer %s: want exit 1 mentioning %q, got:\n%s", args, want, out)
		}
	}
	out, _ := e.tracker(t, "answer")
	if !strings.Contains(out, id) || !strings.Contains(out, "ReviewDiff") {
		t.Errorf("list does not show the waiting run:\n%s", out)
	}
}

const digestDip = `workflow digesttest
  start: Start
  exit: Done

  agent Start
    label: Start

  human Gate
    label: "Go on?"

  tool Work
    command: "true"

  agent Done
    label: Done

  edges
    Start -> Gate
    Gate -> Work  label: "go"
    Work -> Done
`

func TestAnswer_RefusesDigestMismatch(t *testing.T) {
	e := newFirstWinEnv(t)
	dip := filepath.Join(e.repo, "flow.dip")
	if err := os.WriteFile(dip, []byte(digestDip), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := e.tracker(t, "flow.dip"); exitCode(t, err) != 4 {
		t.Fatalf("flow.dip did not pause:\n%s", out)
	}
	id := e.pausedRunID(t)
	if err := os.WriteFile(dip, []byte(strings.Replace(digestDip, `command: "true"`, `command: "echo changed"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.tracker(t, "answer", id, "go")
	if exitCode(t, err) != 1 || !strings.Contains(out, "the workflow changed since run "+id+" paused") {
		t.Fatalf("answer after an edit: want a refusal, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(e.repo, ".tracker", "runs", id, "gate.json")); err != nil {
		t.Errorf("a refused answer must leave the run waiting: %v", err)
	}
}

// RF5: a --json run's stream ends with exactly one terminal event, gate_paused,
// whose message carries the answer command, and no failure events.
func TestAnswerE2E_JSONStream(t *testing.T) {
	e := newFirstWinEnv(t)
	dip := filepath.Join(e.repo, "flow.dip")
	if err := os.WriteFile(dip, []byte(digestDip), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.tracker(t, "--json", "flow.dip")
	if exitCode(t, err) != 4 {
		t.Fatalf("--json run: want exit 4\n%s", out)
	}
	var terminals, failures []string
	var pausedGate map[string]any
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal([]byte(sc.Text()), &ev) != nil {
			continue // the summary printed after the stream
		}
		typ, _ := ev["type"].(string)
		if ts, _ := ev["terminal_status"].(string); ts != "" {
			terminals = append(terminals, typ)
			pausedGate = ev
		}
		if typ == "stage_failed" || typ == "pipeline_failed" {
			failures = append(failures, typ)
		}
	}
	if len(terminals) != 1 || terminals[0] != "gate_paused" {
		t.Fatalf("terminal events = %v, want exactly [gate_paused]\n%s", terminals, out)
	}
	if len(failures) != 0 {
		t.Errorf("failure events in a paused run: %v", failures)
	}
	if res, _ := pausedGate["gate_resolution"].(string); res != "paused" {
		t.Errorf("gate_paused event = %v, want gate_resolution paused", pausedGate)
	}
}

// RF2 (CLI half): after a pause, `tracker -r` with --gates console answers
// the gate from stdin; tracker answer then finds nothing waiting.
func TestAnswerE2E_ConsoleResumeAfterPause(t *testing.T) {
	e := newFirstWinEnv(t)
	dip := filepath.Join(e.repo, "flow.dip")
	if err := os.WriteFile(dip, []byte(digestDip), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := e.tracker(t, "flow.dip"); exitCode(t, err) != 4 {
		t.Fatalf("did not pause:\n%s", out)
	}
	id := e.pausedRunID(t)
	cmd := exec.Command(e.bin, "-r", id, "--gates", "console", "--no-tui", "flow.dip")
	cmd.Dir, cmd.Env = e.repo, e.env
	cmd.Stdin = strings.NewReader("go\n")
	out, err := cmd.CombinedOutput()
	if exitCode(t, err) != 0 {
		t.Fatalf("console resume: exit %d\n%s", exitCode(t, err), out)
	}
	ans, err := e.tracker(t, "answer", id, "go")
	if exitCode(t, err) != 1 || !strings.Contains(ans, "not waiting") {
		t.Errorf("answer after the console resume: want 'not waiting', got:\n%s", ans)
	}
}
