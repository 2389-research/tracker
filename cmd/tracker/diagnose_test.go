package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunDiagnose_SuppressesLibraryWarningsOnStderr(t *testing.T) {
	workdir, runDir := setupTestRun(t, "diag-bad-status")
	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "diag-bad-status",
		"current_node":    "",
		"completed_nodes": []string{"Start"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})
	if err := os.MkdirAll(filepath.Join(runDir, "Build"), 0o755); err != nil {
		t.Fatalf("mkdir node: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "Build", "status.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	oldStdout := os.Stdout
	stdoutR, stdoutW, _ := os.Pipe()
	os.Stdout = stdoutW
	oldStderr := os.Stderr
	stderrR, stderrW, _ := os.Pipe()
	os.Stderr = stderrW

	err := runDiagnose(workdir, "diag-bad-status")

	stdoutW.Close()
	os.Stdout = oldStdout
	stderrW.Close()
	os.Stderr = oldStderr

	if err != nil {
		t.Fatalf("runDiagnose returned error: %v", err)
	}

	stdoutBuf := make([]byte, 8192)
	nOut, _ := stdoutR.Read(stdoutBuf)
	stdout := string(stdoutBuf[:nOut])
	if !strings.Contains(stdout, "diag-bad-status") {
		t.Fatalf("expected diagnose output for run, got:\n%s", stdout)
	}

	stderrBuf := make([]byte, 8192)
	nErr, _ := stderrR.Read(stderrBuf)
	stderr := string(stderrBuf[:nErr])
	if strings.Contains(stderr, "warning: cannot parse") {
		t.Fatalf("expected warning to be suppressed on stderr, got:\n%s", stderr)
	}
}
