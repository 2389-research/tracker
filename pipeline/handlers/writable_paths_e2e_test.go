//go:build linux

package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
)

// TestMain dispatches to the __jail-exec helper when this test binary is
// re-invoked by WrapBashCmd via /proc/self/exe. Without this, the re-exec
// child starts running tests instead of applying Landlock.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__jail-exec" {
		os.Exit(execpkg.RunJailExec(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestWritablePathsEnforcement(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}

	type row struct {
		name           string
		cmdTemplate    string // %s placeholders filled with: inside dir, outside dir
		assertInsideOK bool   // a file at <inside>/ok.txt must exist
		assertOutside  string // expected outcome at <outside>/escape.txt: "denied" or "" (no assertion)
	}

	cases := []row{
		{
			name:          "direct out-of-jail write denied",
			cmdTemplate:   "echo pwned > %s/escape.txt",
			assertOutside: "denied",
		},
		{
			name:          "child process out-of-jail write denied",
			cmdTemplate:   "sh -c 'echo pwned > %s/escape.txt'",
			assertOutside: "denied",
		},
		{
			name:           "in-jail write succeeds",
			cmdTemplate:    "echo allowed > %s/ok.txt",
			assertInsideOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			anchor := t.TempDir()
			workspace := filepath.Join(anchor, "workspace")
			outsideRoot := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(outsideRoot, 0755); err != nil {
				t.Fatal(err)
			}

			env := execpkg.NewLocalEnvironment(anchor)
			cfg := agent.SessionConfig{
				WorkingDir:       anchor,
				WritablePaths:    []string{"workspace/**"},
				WritablePathsSet: true,
				Backend:          "native",
			}
			if _, err := configureJail(&cfg, env, anchor); err != nil {
				t.Fatalf("configureJail: %v", err)
			}

			// Substitute the target dir into the command template.
			var cmd string
			switch {
			case tc.assertInsideOK:
				cmd = fmt.Sprintf(tc.cmdTemplate, workspace)
			case tc.assertOutside != "":
				cmd = fmt.Sprintf(tc.cmdTemplate, outsideRoot)
			default:
				t.Fatalf("test row has neither inside nor outside assertion: %s", tc.name)
			}

			// Run the command through the jailed env. Output ignored; the
			// assertion is on the resulting filesystem state, since shells
			// generally print errors but exit non-zero only on the last command.
			_, _ = env.ExecCommand(context.Background(), "sh", []string{"-c", cmd}, 5*time.Second)

			if tc.assertInsideOK {
				okPath := filepath.Join(workspace, "ok.txt")
				if _, err := os.Stat(okPath); err != nil {
					t.Errorf("inside write was blocked: %v", err)
				}
			}
			if tc.assertOutside == "denied" {
				escapePath := filepath.Join(outsideRoot, "escape.txt")
				if _, err := os.Stat(escapePath); err == nil {
					t.Errorf("outside write succeeded; jail did not enforce. File: %s", escapePath)
				}
			}
		})
	}
}
