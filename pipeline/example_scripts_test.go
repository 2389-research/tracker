package pipeline_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// exampleScriptTestGlobs lists the shell fixture suites that live beside the
// example workflow scripts they exercise. Each `<Name>_test.sh` is a
// self-contained bash suite that prints `ok:`/`FAIL:` lines and exits
// non-zero on any failure (see examples/scripts/build_product/*_test.sh).
// Running them under `go test` makes them a CI gate — the same set
// `make test-scripts` runs directly for fast iteration.
var exampleScriptTestGlobs = []string{
	filepath.Join("..", "examples", "scripts", "*", "*_test.sh"),
	filepath.Join("..", "examples", "scripts", "*", "lib", "*_test.sh"),
	filepath.Join("..", "examples", "subgraphs", "scripts", "*", "*_test.sh"),
}

// exampleScriptTestDeps are the binaries every fixture suite may need. The
// suites execute the scripts under test with `sh` (dippin runs command_file
// via `sh -c`, so the shebang is ignored — tracker #324), init throwaway git
// repos, and the adversarial-review suites parse JSON with jq. ubuntu CI
// images ship all four, so the skip below only fires on minimal local boxes.
var exampleScriptTestDeps = []string{"bash", "sh", "git", "jq"}

func TestExampleScripts(t *testing.T) {
	for _, dep := range exampleScriptTestDeps {
		if _, err := exec.LookPath(dep); err != nil {
			t.Skipf("example script fixtures need %q on PATH: %v", dep, err)
		}
	}
	scripts := collectExampleScriptTests(t)
	for _, script := range scripts {
		t.Run(filepath.Base(script), func(t *testing.T) {
			t.Parallel()
			runExampleScriptTest(t, script)
		})
	}
}

func collectExampleScriptTests(t *testing.T) []string {
	t.Helper()
	var scripts []string
	for _, pattern := range exampleScriptTestGlobs {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %q: %v", pattern, err)
		}
		scripts = append(scripts, matches...)
	}
	if len(scripts) == 0 {
		t.Fatalf("no *_test.sh fixtures matched %v — the glob drifted from the layout", exampleScriptTestGlobs)
	}
	return scripts
}

func runExampleScriptTest(t *testing.T, script string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Base(script))
	cmd.Dir = filepath.Dir(script)
	cmd.Env = hermeticScriptEnv(os.Environ(), t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n--- output ---\n%s", script, err, out)
	}
	if strings.Contains(string(out), "\nFAIL:") || strings.HasPrefix(string(out), "FAIL:") {
		// Belt-and-braces: a suite that forgot to propagate `fail` into its
		// exit code still fails here.
		t.Fatalf("%s printed FAIL lines but exited 0\n--- output ---\n%s", script, out)
	}
}

// gitEnvPassthrough lists the only GIT_* variables a fixture subprocess keeps
// from the parent process. GIT_EXEC_PATH points at this git's own helper
// programs (a tooling location, not repo or commit state), so it is safe to
// keep; every other GIT_* var is stripped so the throwaway repos the fixtures
// create run hermetically.
//
// This matters when the suite runs from inside the pre-commit hook
// (core.hooksPath -> prek -> make test -> go test): git exports GIT_INDEX_FILE
// (a *relative* .git/index), GIT_PREFIX and GIT_AUTHOR_* for the hook, and they
// are inherited all the way down. A relative GIT_INDEX_FILE re-resolved by git
// inside a `git worktree add` target — whose .git is a *file*, not a directory —
// fails with "index file open failed: Not a directory"; an inherited
// GIT_AUTHOR_* overrides the deterministic identity the build scripts set,
// breaking the identity fixtures. Neither reproduces under a bare `go test`
// (no hook, so no such vars), so stripping restores that clean baseline.
var gitEnvPassthrough = map[string]bool{
	"GIT_EXEC_PATH": true,
}

// hermeticScriptEnv builds the environment for a fixture subprocess: the parent
// environment minus every GIT_* var except gitEnvPassthrough, plus a throwaway
// HOME and neutralized global/system git config. This keeps the throwaway git
// repos the fixtures create from picking up the developer's global config or
// hooks — or the enclosing pre-commit hook's git state.
func hermeticScriptEnv(parentEnv []string, home string) []string {
	env := make([]string, 0, len(parentEnv)+3)
	for _, kv := range parentEnv {
		if name, _, ok := strings.Cut(kv, "="); ok &&
			strings.HasPrefix(name, "GIT_") && !gitEnvPassthrough[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
}

func TestHermeticScriptEnvStripsGitState(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"GIT_INDEX_FILE=.git/index", // relative; breaks `git worktree add` (ENOTDIR)
		"GIT_DIR=/somewhere/.git",
		"GIT_PREFIX=",
		"GIT_WORK_TREE=/somewhere",
		"GIT_AUTHOR_NAME=Somebody",              // would override a script's commit identity
		"GIT_AUTHOR_EMAIL=somebody@example.com", //
		"GIT_EXEC_PATH=/opt/git/libexec",        // git's own helpers — kept
		"KEEP=1",
	}
	got := hermeticScriptEnv(parent, "/tmp/home")

	// No parent GIT_* state/identity var may reach the fixtures; only
	// GIT_EXEC_PATH (git's helper location) and the hermetic config survive.
	// Regression guard for the failures that appear only under the pre-commit
	// hook, where git exports GIT_INDEX_FILE / GIT_PREFIX / GIT_AUTHOR_*.
	for _, kv := range got {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "GIT_") {
			continue
		}
		switch name {
		case "GIT_EXEC_PATH", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM": // allowed
		default:
			t.Errorf("hermeticScriptEnv leaked git state var into fixture env: %q", kv)
		}
	}

	// Non-git vars survive, GIT_EXEC_PATH is kept, and the hermetic overrides
	// are present.
	for _, want := range []string{
		"PATH=/usr/bin", "KEEP=1", "GIT_EXEC_PATH=/opt/git/libexec",
		"HOME=/tmp/home", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("hermeticScriptEnv missing expected entry %q; got %v", want, got)
		}
	}
}
