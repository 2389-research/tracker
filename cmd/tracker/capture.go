// ABOUTME: Run-capture spec recording — captures the exact spec the process is executing, at load time.
// ABOUTME: The spec is handed to the library via Config.Capture (buildCaptureConfig), which writes the artifacts once the run ID exists.
package main

import (
	"github.com/2389-research/dippin-lang/ir"
)

// capturedSpec holds the specification the current process is executing,
// recorded when the pipeline file is read.
//
// Package-level state: one process runs one pipeline, and the spec is recorded
// deep in the load path (loadPipeline/loadEmbeddedPipeline), far from the
// runOptions built in executeRun. The alternative is threading two telemetry
// fields through the whole load-to-run call chain for telemetry's sake.
//
// Captured at load rather than re-read at the end on purpose. The run ID does
// not exist until the engine returns, so the write has to happen late — but
// re-reading the file then would record whatever it says at that moment, which
// is the wrong document if anyone edited it mid-run. These are the bytes that
// actually executed. buildCaptureConfig reads this into a tracker.CaptureConfig,
// and the library writes the spec + run.json inside Engine.Run.
var capturedSpec struct {
	path     string
	source   string
	workflow *ir.Workflow
}

// recordExecutedSpec stores the spec for the run about to start.
func recordExecutedSpec(path, source string, workflow *ir.Workflow) {
	capturedSpec.path = path
	capturedSpec.source = source
	capturedSpec.workflow = workflow
}
