package pipeline_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	// A hermetic HOME/env keeps the throwaway git repos the fixtures create
	// from picking up the developer's global config or hooks.
	cmd.Env = append(os.Environ(),
		"HOME="+t.TempDir(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
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
