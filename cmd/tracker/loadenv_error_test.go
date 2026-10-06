package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeUnreadableEnv points config resolution at an empty dir and puts an
// unreadable .env (a directory named ".env") in workdir so the parse fails,
// forcing the loader to return an error deterministically.
func makeUnreadableEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TRACKER_ENV_FILES", "")
	workdir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workdir, ".env"), 0o700); err != nil {
		t.Fatalf("mkdir .env: %v", err)
	}
	return workdir
}

// makeUnreadableConfigEnv puts the unreadable .env at the CONFIG path instead.
// `tracker version` loads only that file (#659), so this is the failure it
// must propagate.
func makeUnreadableConfigEnv(t *testing.T) {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("TRACKER_ENV_FILES", "")
	if err := os.MkdirAll(filepath.Join(configHome, "tracker", ".env"), 0o700); err != nil {
		t.Fatalf("mkdir config .env dir: %v", err)
	}
}

func TestLoadEnvFilesPropagatesReadError(t *testing.T) {
	workdir := makeUnreadableEnv(t)
	if err := loadEnvFilesMode(workdir, ""); err == nil {
		t.Fatal("loadEnvFilesMode = nil, want error for unreadable .env")
	}
}

func TestExecuteDoctorPropagatesLoadEnvError(t *testing.T) {
	workdir := makeUnreadableEnv(t)
	if err := executeDoctor(runConfig{workdir: workdir}); err == nil {
		t.Fatal("executeDoctor = nil, want error from loadEnvFiles")
	}
}

func TestExecuteVersionPropagatesLoadEnvError(t *testing.T) {
	makeUnreadableConfigEnv(t)
	if err := executeVersion(runConfig{}); err == nil {
		t.Fatal("executeVersion = nil, want error from the config .env load")
	}
}
