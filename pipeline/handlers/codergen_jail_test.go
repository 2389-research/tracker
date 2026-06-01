package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
)

func TestConfigureJail_NotSet_NoOp(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    nil,
		WritablePathsSet: false,
		Backend:          "native",
	}
	enabled, err := configureJail(&cfg, env, "/home/user/run")
	if err != nil {
		t.Fatalf("configureJail = %v, want nil for unset", err)
	}
	if enabled {
		t.Error("configureJail reported enabled=true with WritablePathsSet=false")
	}
	if env.CommandWrapper != nil || env.WriteOpener != nil {
		t.Error("env hooks set when jail not enabled")
	}
}

func TestConfigureJail_SetButEmpty_FailsClosed(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    nil,
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with Set=true + empty paths = nil; want fail-closed")
	}
	// Validation should surface that the paths list is empty.
	if !strings.Contains(strings.ToLower(err.Error()), "empty") {
		t.Errorf("err = %v, want substring 'empty'", err)
	}
}

func TestConfigureJail_RefusesOnClaudeCode(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "claude-code",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with claude-code backend = nil; want refuse")
	}
	if !strings.Contains(err.Error(), "claude-code") {
		t.Errorf("err = %v, want message naming claude-code backend", err)
	}
}

func TestConfigureJail_RefusesOnAcp(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "acp",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with acp backend = nil; want refuse")
	}
	if !strings.Contains(err.Error(), "acp") {
		t.Errorf("err = %v, want message naming acp backend", err)
	}
}

func TestConfigureJail_RefusesOnUnknownBackend(t *testing.T) {
	// Unknown backend names fail-closed; safer to refuse than silently no-op
	// on a future backend that doesn't enforce.
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "future-backend-xyz",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with unknown backend = nil; want refuse")
	}
	if !strings.Contains(err.Error(), "future-backend-xyz") {
		t.Errorf("err = %v, want message naming unknown backend", err)
	}
}

func TestConfigureJail_RefusesOnInvalidPaths(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"/etc/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if !errors.Is(err, execpkg.ErrPathEscape) {
		t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
	}
}

func TestConfigureJail_HappyPathWiresEnv(t *testing.T) {
	if probeErr := execpkg.ProbeLandlock(); probeErr != nil {
		t.Skipf("Landlock unavailable: %v", probeErr)
	}
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	enabled, err := configureJail(&cfg, env, "/home/user/run")
	if err != nil {
		t.Fatalf("configureJail = %v, want nil", err)
	}
	if !enabled {
		t.Error("configureJail reported enabled=false on happy path")
	}
	if env.CommandWrapper == nil {
		t.Error("env.CommandWrapper not set")
	}
	if env.WriteOpener == nil {
		t.Error("env.WriteOpener not set")
	}
}

func TestConfigureJail_RefusesOnNoLandlock_SimulatedNonLinux(t *testing.T) {
	if probeErr := execpkg.ProbeLandlock(); probeErr == nil {
		t.Skip("Landlock available on this host; cannot exercise the no-Landlock refuse path")
	}
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if !errors.Is(err, execpkg.ErrLandlockUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrLandlockUnavailable)", err)
	}
}

func TestMatchWritablePath(t *testing.T) {
	cases := []struct {
		name    string
		relPath string
		globs   []string
		want    bool
	}{
		{
			name:    "exact match — directory glob",
			relPath: "workspace/foo.txt",
			globs:   []string{"workspace/**"},
			want:    true,
		},
		{
			name:    "deep match — directory glob",
			relPath: "workspace/a/b/c.txt",
			globs:   []string{"workspace/**"},
			want:    true,
		},
		{
			name:    "directory itself — directory glob",
			relPath: "workspace",
			globs:   []string{"workspace/**"},
			want:    true,
		},
		{
			name:    "exact file glob — matches",
			relPath: "workspace/out.md",
			globs:   []string{"workspace/out.md"},
			want:    true,
		},
		{
			name:    "exact file glob — does NOT match other file",
			relPath: "workspace/other.md",
			globs:   []string{"workspace/out.md"},
			want:    false,
		},
		{
			name:    "single-segment * glob — matches",
			relPath: "workspace/foo.md",
			globs:   []string{"workspace/*.md"},
			want:    true,
		},
		{
			name:    "single-segment * glob — does NOT match deeper path",
			relPath: "workspace/sub/foo.md",
			globs:   []string{"workspace/*.md"},
			want:    false,
		},
		{
			name:    "multiple globs — second matches",
			relPath: ".ai/sprints/2026/A.json",
			globs:   []string{"workspace/**", ".ai/sprints/**"},
			want:    true,
		},
		{
			name:    "no glob matches",
			relPath: "etc/passwd",
			globs:   []string{"workspace/**"},
			want:    false,
		},
		{
			name:    "** alone matches anything",
			relPath: "anything/at/all",
			globs:   []string{"**"},
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchWritablePath(tc.relPath, tc.globs)
			if got != tc.want {
				t.Errorf("matchWritablePath(%q, %v) = %v, want %v", tc.relPath, tc.globs, got, tc.want)
			}
		})
	}
}
