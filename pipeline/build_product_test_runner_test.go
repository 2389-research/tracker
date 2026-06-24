// ABOUTME: Regression guard for issue #305 — TestMilestone and FinalBuild must run
// ABOUTME: EVERY detected build stack (polyglot repos), not just the first match.
package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// toolCmd returns the named tool node's tool_command from the loaded
// build_product graph (the exact bytes tracker executes at runtime).
func toolCmd(t *testing.T, nodeID string) string {
	t.Helper()
	g := loadBuildProduct(t)
	n, ok := g.Nodes[nodeID]
	if !ok {
		t.Fatalf("%s node missing from build_product graph", nodeID)
	}
	cmd := n.Attrs["tool_command"]
	if cmd == "" {
		t.Fatalf("%s node has an empty tool_command attr (schema change? not a tool node?)", nodeID)
	}
	return cmd
}

// writeStub writes a fake toolchain binary that logs its invocation to
// $STUB_LOG and exits with $STUB_<NAME>_EXIT (default 0). Stubs shadow any
// real toolchain via PATH ordering, so `go test` / `npm test` etc. are
// deterministic and offline.
// The go stub additionally honors $STUB_GO_TEST_EXIT for `go test` only, so
// a case can fail the test sweep while `go build` still passes.
func writeStub(t *testing.T, binDir, name string) {
	t.Helper()
	upper := strings.ToUpper(name)
	script := "#!/bin/sh\n" +
		"echo \"" + name + " $*\" >> \"$STUB_LOG\"\n"
	if name == "go" {
		script += "case \"${1:-}\" in test) exit \"${STUB_GO_TEST_EXIT:-${STUB_GO_EXIT:-0}}\";; esac\n"
	}
	script += "exit \"${STUB_" + upper + "_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// stackEnv builds an env with stubbed go/npm/uv/cargo first on PATH (real
// coreutils stay reachable for cat/grep/paste), plus the stub log path and
// any extra STUB_*_EXIT overrides.
func stackEnv(t *testing.T, stubLog string, extra ...string) []string {
	t.Helper()
	binDir := t.TempDir()
	for _, name := range []string{"go", "npm", "uv", "cargo"} {
		writeStub(t, binDir, name)
	}
	// Prepend the stub bin to the host PATH: the stubs still shadow any real
	// toolchain (first match wins) while coreutils (grep/paste/cat) stay
	// reachable wherever the host keeps them (Copilot, PR #345).
	env := []string{
		"PATH=" + binDir + ":" + os.Getenv("PATH"),
		"STUB_LOG=" + stubLog,
		"HOME=" + t.TempDir(),
	}
	return append(env, extra...)
}

// extractHeredoc returns the body of the `<<'EOF' … EOF` heredoc written into
// `path` by `cmd`, i.e. the bytes between `cat > <path> <<'<term>'` and the
// terminator line. Used to provision the SAME .ai/build/verify.sh that Setup
// writes at runtime, so the TestMilestone suite exercises the real shared gate
// (issue #406 — single source of truth) instead of a hand-copied duplicate.
func extractHeredoc(t *testing.T, cmd, path, term string) string {
	t.Helper()
	open := "cat > " + path + " <<'" + term + "'"
	lines := strings.Split(cmd, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == open {
			start = i + 1
			break
		}
	}
	if start == -1 {
		t.Fatalf("heredoc opener %q not found in tool_command", open)
	}
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == term {
			return strings.Join(lines[start:i], "\n") + "\n"
		}
	}
	t.Fatalf("heredoc terminator %q not found after opener", term)
	return ""
}

// setupRunDir creates a workdir with the .ai scaffolding both tool nodes
// expect: a no-op ci-probe.sh (the #299 gate is covered by its own suite),
// the .ai/build/verify.sh shared green-gate extracted from Setup (the script
// TestMilestone now delegates to — issue #406), and the milestones dir
// TestMilestone writes its attempt counter into.
func setupRunDir(t *testing.T, stackFiles ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{".ai/build", ".ai/milestones"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(dir, ".ai/build/ci-probe.sh"),
		"run_project_ci_gate() { return \"${STUB_CI_RC:-0}\"; }\n")
	mustWrite(t, filepath.Join(dir, ".ai/build/verify.sh"),
		extractHeredoc(t, toolCmd(t, "Setup"), ".ai/build/verify.sh", "VERIFY_EOF"))
	for _, f := range stackFiles {
		mustWrite(t, filepath.Join(dir, f), "{}\n")
	}
	return dir
}

// runToolCmd executes a tool_command body in dir and returns combined output
// + exit code (the same sh semantics the tool handler uses).
func runToolCmd(t *testing.T, cmd, dir string, env []string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available; skipping runtime tool-command test")
	}
	scriptPath := filepath.Join(t.TempDir(), "cmd.sh")
	if err := os.WriteFile(scriptPath, []byte(cmd), 0o644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("sh", scriptPath)
	c.Dir = dir
	c.Env = env
	b, err := c.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("exec failed: %v\n%s", err, string(b))
		}
		code = ee.ExitCode()
	}
	return string(b), code
}

// readLog returns the stub invocation log ("" when no stub ran).
func readLog(t *testing.T, stubLog string) string {
	t.Helper()
	b, err := os.ReadFile(stubLog)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(b)
}

// --- TestMilestone ---

// A Go+JS repo must run BOTH `go test` and `npm test` (#305 — the pre-fix
// first-match elif chain never reaches npm).
func TestMilestoneRunsAllDetectedStacks(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, code := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, stackEnv(t, stubLog))
	if code != 0 {
		t.Fatalf("all stacks pass but exit=%d:\n%s", code, out)
	}
	log := readLog(t, stubLog)
	if !strings.Contains(log, "go test") {
		t.Errorf("go test not invoked in Go+JS repo:\n%s", log)
	}
	if !strings.Contains(log, "npm test") {
		t.Errorf("npm test not invoked in Go+JS repo (#305 first-match bug):\n%s", log)
	}
	if !strings.Contains(out, "tests-pass") {
		t.Errorf("missing tests-pass sentinel:\n%s", out)
	}
}

// A failure in the SECOND detected stack must fail the milestone.
func TestMilestoneSecondStackFailureFails(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	env := stackEnv(t, stubLog, "STUB_NPM_EXIT=7")
	out, code := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, env)
	if code == 0 {
		t.Fatalf("npm test failed but TestMilestone exited 0 (#305 masking):\n%s", out)
	}
	if strings.Contains(out, "tests-pass") {
		t.Errorf("tests-pass sentinel emitted despite npm failure:\n%s", out)
	}
	// First attempt — normal failure for the fix loop, not escalation.
	if strings.Contains(out, "escalate") {
		t.Errorf("first failure should not escalate:\n%s", out)
	}
}

// All four stacks present → all four runners invoked.
func TestMilestoneRunsAllFourStacks(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json", "pyproject.toml", "Cargo.toml")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, code := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, stackEnv(t, stubLog))
	if code != 0 {
		t.Fatalf("all stacks pass but exit=%d:\n%s", code, out)
	}
	log := readLog(t, stubLog)
	for _, want := range []string{"go test", "npm test", "uv run pytest", "cargo test"} {
		if !strings.Contains(log, want) {
			t.Errorf("%q not invoked with all four stack files present:\n%s", want, log)
		}
	}
}

// Single-stack repos behave exactly as before (regression pin): only the
// matching runner is invoked.
func TestMilestoneSingleStackUnchanged(t *testing.T) {
	dir := setupRunDir(t, "go.mod")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, code := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, stackEnv(t, stubLog))
	if code != 0 {
		t.Fatalf("single-stack pass but exit=%d:\n%s", code, out)
	}
	log := readLog(t, stubLog)
	if !strings.Contains(log, "go test") {
		t.Errorf("go test not invoked:\n%s", log)
	}
	for _, notWant := range []string{"npm", "uv", "cargo"} {
		if strings.Contains(log, notWant) {
			t.Errorf("%q invoked in a Go-only repo:\n%s", notWant, log)
		}
	}
	if !strings.Contains(out, "tests-pass") {
		t.Errorf("missing tests-pass sentinel:\n%s", out)
	}
}

// The known_failures skip pattern is still applied to the Go runner.
func TestMilestoneKnownFailuresSkipPreserved(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json")
	mustWrite(t, filepath.Join(dir, ".ai/milestones/known_failures"),
		"# a comment\nTestFlaky\n\nTestAlsoFlaky\n")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, code := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, stackEnv(t, stubLog))
	if code != 0 {
		t.Fatalf("exit=%d:\n%s", code, out)
	}
	log := readLog(t, stubLog)
	if !strings.Contains(log, "-skip TestFlaky|TestAlsoFlaky") {
		t.Errorf("go test missing -skip pattern:\n%s", log)
	}
	if !strings.Contains(log, "npm test") {
		t.Errorf("npm test not invoked alongside skip-patterned go test:\n%s", log)
	}
}

// A FIRST-stack test failure (go test fails, go build passes) must not
// abort before later stacks run, and must still fail the milestone —
// symmetric with the FinalBuild accumulate case (Copilot, PR #345).
func TestMilestoneFirstStackFailureStillRunsLater(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	env := stackEnv(t, stubLog, "STUB_GO_TEST_EXIT=7")
	out, code := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, env)
	if code == 0 {
		t.Fatalf("go test failed but TestMilestone exited 0:\n%s", out)
	}
	if strings.Contains(out, "tests-pass") {
		t.Errorf("tests-pass sentinel emitted despite go test failure:\n%s", out)
	}
	log := readLog(t, stubLog)
	if !strings.Contains(log, "npm test") {
		t.Errorf("npm test should still run after go test failure (#305 accumulate):\n%s", log)
	}
}

// PR #411 finding #1 (Codex/Copilot/CodeRabbit consensus): verify.sh exit code 2
// is RESERVED for "Makefile present but `make` not installed" — TestMilestone
// routes exit 2 straight to `escalate`. A language test runner can legitimately
// exit 2 (pytest collection error, an arbitrary npm script), which must NOT be
// mistaken for that env-missing escalate. verify.sh must collapse every non-zero
// TEST runner exit to 1, reserving 2 for the CI/Makefile path. Run the extracted
// verify.sh directly so the assertion pins the source of the exit code.
func TestVerifyScriptCollapsesTestRunnerExit2(t *testing.T) {
	dir := setupRunDir(t, "package.json")
	verify := extractHeredoc(t, toolCmd(t, "Setup"), ".ai/build/verify.sh", "VERIFY_EOF")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, code := runToolCmd(t, verify, dir, stackEnv(t, stubLog, "STUB_NPM_EXIT=2"))
	if code == 0 {
		t.Fatalf("npm test failed (exit 2) but verify.sh exited 0:\n%s", out)
	}
	if code == 2 {
		t.Errorf("npm test exited 2 (a legitimate test-runner failure) but verify.sh propagated exit 2, which TestMilestone treats as `make` missing → escalate; a normal fixable failure gets falsely escalated (PR #411 finding #1):\n%s", out)
	}
}

// PR #411 finding #1 at the routing layer: a test runner exiting 2 must route to
// the fix loop (no sentinel), never to EscalateMilestone via the `escalate`
// sentinel that exit 2 (`make` missing) would emit.
func TestMilestoneTestRunnerExit2DoesNotFalselyEscalate(t *testing.T) {
	dir := setupRunDir(t, "package.json")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, _ := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, stackEnv(t, stubLog, "STUB_NPM_EXIT=2"))
	if strings.Contains(out, "escalate") {
		t.Errorf("a test runner exiting 2 falsely escalated (the `escalate` sentinel is reserved for `make` missing / fix-loop exhaustion) instead of routing to the fix loop (PR #411 finding #1):\n%s", out)
	}
}

// No stack files → the no-build-system notice still prints and the node passes.
func TestMilestoneNoStackNoticePreserved(t *testing.T) {
	dir := setupRunDir(t)
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, code := runToolCmd(t, toolCmd(t, "TestMilestone"), dir, stackEnv(t, stubLog))
	if code != 0 {
		t.Fatalf("no-stack repo should pass, exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "no known build system") {
		t.Errorf("missing no-build-system notice:\n%s", out)
	}
}

// --- FinalBuild ---

// A Go+JS repo must run BOTH stacks in FinalBuild.
func TestFinalBuildRunsAllDetectedStacks(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	out, code := runToolCmd(t, toolCmd(t, "FinalBuild"), dir, stackEnv(t, stubLog))
	if code != 0 {
		t.Fatalf("all stacks pass but exit=%d:\n%s", code, out)
	}
	log := readLog(t, stubLog)
	if !strings.Contains(log, "go test") {
		t.Errorf("go test not invoked in Go+JS repo:\n%s", log)
	}
	if !strings.Contains(log, "npm test") {
		t.Errorf("npm test not invoked in Go+JS repo (#305 first-match bug):\n%s", log)
	}
	if !strings.Contains(out, "final-build-pass") {
		t.Errorf("missing final-build-pass sentinel:\n%s", out)
	}
}

// A failing stack fails FinalBuild AND the remaining stacks still run
// (failures accumulate; no early abort that would hide later-stack results).
func TestFinalBuildStackFailureFailsButRunsRemaining(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	env := stackEnv(t, stubLog, "STUB_NPM_EXIT=7")
	out, code := runToolCmd(t, toolCmd(t, "FinalBuild"), dir, env)
	if code == 0 {
		t.Fatalf("npm test failed but FinalBuild exited 0 (#305 masking):\n%s", out)
	}
	if strings.Contains(out, "final-build-pass") {
		t.Errorf("final-build-pass emitted despite npm failure:\n%s", out)
	}
	log := readLog(t, stubLog)
	if !strings.Contains(log, "go test") || !strings.Contains(log, "npm test") {
		t.Errorf("both stacks should have run:\n%s", log)
	}
}

// A FIRST-stack test failure must not abort before later stacks run, and
// must still fail the node.
func TestFinalBuildFirstStackFailureStillRunsLater(t *testing.T) {
	dir := setupRunDir(t, "go.mod", "package.json")
	stubLog := filepath.Join(t.TempDir(), "stub.log")
	// Fail only `go test` (build passes) so the sweep reaches npm.
	env := stackEnv(t, stubLog, "STUB_GO_TEST_EXIT=7")
	out, code := runToolCmd(t, toolCmd(t, "FinalBuild"), dir, env)
	if code == 0 {
		t.Fatalf("go test failed but FinalBuild exited 0:\n%s", out)
	}
	log := readLog(t, stubLog)
	if !strings.Contains(log, "npm test") {
		t.Errorf("npm test should still run after go test failure (#305 accumulate):\n%s", log)
	}
}
