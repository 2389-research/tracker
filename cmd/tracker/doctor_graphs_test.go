// ABOUTME: tracker doctor hands the loaded pipeline (subgraphs included) to the
// ABOUTME: library so its backend checks see every agent node (#716).
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorGraphOpts(t *testing.T) {
	if opts := doctorGraphOpts(""); opts != nil {
		t.Fatal("no pipeline: want no option")
	}
	if opts := doctorGraphOpts(filepath.Join(t.TempDir(), "missing.dip")); opts != nil {
		t.Fatal("unloadable pipeline: want no option (the Pipeline File check reports it)")
	}
	dip := filepath.Join(t.TempDir(), "p.dip")
	src := "workflow P\n  goal: \"p\"\n  start: Start\n  exit: Done\n\n  agent Start\n    label: Start\n\n  agent Work\n    backend: auto\n    prompt: \"work\"\n\n  agent Done\n    label: Done\n\n  edges\n    Start -> Work\n    Work -> Done\n"
	if err := os.WriteFile(dip, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if opts := doctorGraphOpts(dip); len(opts) != 1 {
		t.Fatalf("loadable pipeline: got %d options, want 1", len(opts))
	}
}
