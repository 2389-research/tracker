// ABOUTME: Tests the starter-SPEC.md scaffolding that makes `tracker init` a
// ABOUTME: zero-prerequisite path for the build_product family (#456).
package main

import (
	"errors"
	"strings"
	"testing"
)

func TestScaffoldStarterSpec_WritesForBuildProduct(t *testing.T) {
	var wrotePath string
	var wroteBody []byte
	write := func(p string, b []byte) error { wrotePath, wroteBody = p, b; return nil }
	exists := func(string) bool { return false }

	for _, wf := range []string{"build_product", "build_product_with_superspec"} {
		wrotePath, wroteBody = "", nil
		got, err := scaffoldStarterSpec(wf, exists, write)
		if err != nil {
			t.Fatalf("%s: %v", wf, err)
		}
		if got != starterSpecFile || wrotePath != starterSpecFile {
			t.Errorf("%s: wrote %q / returned %q, want %q", wf, wrotePath, got, starterSpecFile)
		}
		if !strings.Contains(string(wroteBody), "greet") {
			t.Errorf("%s: starter spec should be a buildable example, got:\n%s", wf, wroteBody)
		}
	}
}

func TestScaffoldStarterSpec_NeverOverwrites(t *testing.T) {
	writeCalled := false
	write := func(string, []byte) error { writeCalled = true; return nil }
	exists := func(string) bool { return true } // SPEC.md already present

	got, err := scaffoldStarterSpec("build_product", exists, write)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" || writeCalled {
		t.Errorf("must not overwrite an existing SPEC.md (returned %q, wrote=%v)", got, writeCalled)
	}
}

func TestScaffoldStarterSpec_SkipsWorkflowsWithoutSpec(t *testing.T) {
	writeCalled := false
	write := func(string, []byte) error { writeCalled = true; return nil }
	got, err := scaffoldStarterSpec("ask_and_execute", func(string) bool { return false }, write)
	if err != nil || got != "" || writeCalled {
		t.Errorf("ask_and_execute needs no spec: got %q, err %v, wrote=%v", got, err, writeCalled)
	}
}

func TestScaffoldStarterSpec_PropagatesWriteError(t *testing.T) {
	wantErr := errors.New("disk full")
	_, err := scaffoldStarterSpec("build_product", func(string) bool { return false },
		func(string, []byte) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Errorf("write error should propagate, got %v", err)
	}
}
