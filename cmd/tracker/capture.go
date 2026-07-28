// ABOUTME: Run-capture hooks — records the executed spec and writes run.json when a run ends.
// ABOUTME: Runs after the engine returns, which is the first point the run ID exists.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/tracker/pipeline"
)

// capturedSpec holds the specification the current process is executing,
// recorded when the pipeline file is read.
//
// Package-level state, matching activeArtifactDir and activeExportBundle in
// run.go: one process runs one pipeline, and the alternative is threading two
// fields through the whole load-to-run call chain for telemetry's sake.
//
// Captured at load rather than re-read at the end on purpose. The run ID does
// not exist until the engine returns, so the write has to happen late — but
// re-reading the file then would record whatever it says at that moment, which
// is the wrong document if anyone edited it mid-run. These are the bytes that
// actually executed.
var capturedSpec struct {
	path     string
	source   string
	workflow *ir.Workflow
	bundleID string
}

// recordExecutedSpec stores the spec for the run about to start.
func recordExecutedSpec(path, source string, workflow *ir.Workflow) {
	capturedSpec.path = path
	capturedSpec.source = source
	capturedSpec.workflow = workflow
}

// recordBundleIdentity stores the .dipx identity for a bundle run.
func recordBundleIdentity(id string) {
	capturedSpec.bundleID = id
}

// finalizeRunCapture writes the spec artifacts and run.json for a finished run.
//
// Called from the same place as maybeExportBundle — after the engine returns,
// the first point at which the run ID is known. Failures warn and continue:
// losing telemetry is not worth failing a run that already did its work, and a
// run whose manifest is missing can be rebuilt later with `tracker run-json`.
func finalizeRunCapture(artifactBase, runID string) {
	if runID == "" {
		return
	}
	runDir := filepath.Join(artifactBase, runID)
	if _, err := os.Stat(runDir); err != nil {
		// No run directory means the run never wrote artifacts (a validation
		// failure before execution, say). Nothing to describe.
		return
	}

	if capturedSpec.source != "" || capturedSpec.workflow != nil {
		if _, err := pipeline.WriteSpecArtifacts(runDir, pipeline.SpecArtifacts{
			SourcePath:     capturedSpec.path,
			Source:         capturedSpec.source,
			Workflow:       capturedSpec.workflow,
			BundleIdentity: capturedSpec.bundleID,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: spec capture failed: %v\n", err)
		}
	}

	if _, err := pipeline.WriteRunManifest(runDir, runID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: run manifest failed: %v\n", err)
	}
}
