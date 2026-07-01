package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeUnreadableEnv points config resolution at an empty dir and puts an
// unreadable .env (a directory named ".env") in workdir so godotenv.Read
// fails, forcing loadEnvFiles to return an error deterministically.
func makeUnreadableEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workdir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workdir, ".env"), 0o700); err != nil {
		t.Fatalf("mkdir .env: %v", err)
	}
	return workdir
}

func TestLoadEnvFilesPropagatesReadError(t *testing.T) {
	workdir := makeUnreadableEnv(t)
	if err := loadEnvFiles(workdir); err == nil {
		t.Fatal("loadEnvFiles = nil, want error for unreadable .env")
	}
}

func TestExecuteDoctorPropagatesLoadEnvError(t *testing.T) {
	workdir := makeUnreadableEnv(t)
	if err := executeDoctor(runConfig{workdir: workdir}); err == nil {
		t.Fatal("executeDoctor = nil, want error from loadEnvFiles")
	}
}

func TestExecuteVersionPropagatesLoadEnvError(t *testing.T) {
	workdir := makeUnreadableEnv(t)
	t.Chdir(workdir)
	if err := executeVersion(); err == nil {
		t.Fatal("executeVersion = nil, want error from loadEnvFiles")
	}
}
