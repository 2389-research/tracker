// ABOUTME: Config.Capture seam — the library-owned wiring that makes tracker.Run produce the same on-disk run capture (run.json + spec artifacts + identity-bearing activity.jsonl) the CLI does.
// ABOUTME: Wires one JSONLEventHandler across EventHandler/AgentEvents/LLMTrace with the SessionOwned de-dup correct, so embedders never hit the double-log trap or hand-compose the three seams.
package tracker

import (
	"os"
	"path/filepath"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// CaptureConfig turns on run capture for a library run. Set Config.Capture to a
// (possibly empty) value and the library produces the same artifacts the CLI
// does — run.json, the spec (source + expanded IR + redacted params), and an
// identity-bearing activity.jsonl — without the caller wiring a
// JSONLEventHandler across EventHandler, AgentEvents, and LLMTrace, and without
// tripping the SessionOwned double-log trap that hand-wiring invites.
//
// The zero value is enough: on the Run / NewEngineWithContext path the library
// fills Source and Workflow from the source it parses. The raw
// EventHandler/AgentEvents/LLMTrace seams keep working alongside capture — the
// capture handler is combined with whatever the caller also set.
type CaptureConfig struct {
	// Source is the .dip verbatim, stored as a spec artifact (workflow.dip).
	// Left empty on the Run/NewEngineWithContext path, the library fills it with
	// the source string it parsed.
	Source string
	// SourcePath is where Source was read from, recorded for display.
	SourcePath string
	// Workflow is the expanded IR, stored as workflow.ir.json. dippin expands
	// subgraphs at compile time, so the expanded IR — not the authored source
	// alone — is what explains the run's events. Left nil on the
	// Run/NewEngineWithContext path, the library fills it by parsing.
	Workflow *ir.Workflow
	// CaptureRawLLM stores raw provider streaming chunks in activity.jsonl. Off
	// by default: per-token raw chunks are debugging payload, not run telemetry.
	// The CLI wires this from --verbose.
	CaptureRawLLM bool
	// ForcedBundleMismatch, when set, makes capture write a
	// bundle_mismatch_forced audit entry at run-start (before any engine event),
	// recording that a resume proceeded against a different .dipx bundle than its
	// checkpoint claimed. Nil for new runs and matched resumes.
	ForcedBundleMismatch *ForcedBundleMismatch
}

// ForcedBundleMismatch records that a resume ran against a bundle whose
// content-addressed identity differs from the one stored in the checkpoint,
// allowed through by an explicit override. Both identities are preserved so a
// post-hoc auditor sees exactly what was overridden.
type ForcedBundleMismatch struct {
	RunID            string // resume run ID (needed to open the correct activity log)
	OriginalIdentity string // identity stored in the checkpoint ("sha256:<hex>" or "")
	CurrentIdentity  string // identity of the bundle actually executed ("sha256:<hex>" or "")
}

// captureState is the library's live capture wiring for one run: the handler
// that writes activity.jsonl, the artifact base it snapshots to, and the spec
// to finalize once the run ID is known.
type captureState struct {
	handler      *pipeline.JSONLEventHandler
	artifactBase string
	spec         pipeline.SpecArtifacts
	// costLimited records whether ANY --max-cost ceiling was configured, so
	// finalizeCapture can warn when the run consumed unpriced usage the ceiling
	// could not bound (#518). It is computed from the *resolved* budget
	// (ResolveBudgetLimits), so it covers both an explicit CLI --max-cost /
	// library Config.Budget value and a ceiling that comes only from the .dip
	// `defaults:` block — the latter is a documented, supported way to set the
	// ceiling, and #518 exists precisely to make its silent bypass visible.
	costLimited bool
}

// setupCapture wires the capture handler into cfg when Config.Capture is set,
// returning the live state for finalize/close. It mutates cfg: it defaults an
// empty ArtifactDir (so both the engine and the capture handler write under the
// same base, matching the CLI's <workdir>/.tracker/runs) and combines the
// capture handler with any EventHandler/AgentEvents/LLMTrace the caller already
// set. Returns nil (a no-op) when capture is off.
//
// This is the single wiring path shared by the library and the CLI, so the two
// cannot drift: the CLI sets Config.Capture and only its presentation handlers,
// and this function adds the JSONL/spec/finalize surface for both.
//
// graph is read only to resolve the effective --max-cost ceiling (via
// ResolveBudgetLimits) for the captureState.costLimited flag, so a ceiling that
// comes only from the .dip `defaults:` block still arms the #518 unpriced
// warning.
func setupCapture(cfg *Config, workDir string, graph *pipeline.Graph) *captureState {
	if cfg.Capture == nil {
		return nil
	}
	if cfg.ArtifactDir == "" {
		cfg.ArtifactDir = filepath.Join(workDir, ".tracker", "runs")
	}
	h := pipeline.NewJSONLEventHandler(cfg.ArtifactDir)
	h.SetCaptureRawLLM(cfg.Capture.CaptureRawLLM)
	h.SetBundleIdentity(cfg.BundleIdentity)
	if fm := cfg.Capture.ForcedBundleMismatch; fm != nil {
		// Written before the engine fires, so the audit trail carries the
		// override signal even though the engine event chain has not started.
		h.WriteBundleMismatchForced(fm.RunID, fm.OriginalIdentity, fm.CurrentIdentity)
	}

	cfg.EventHandler = combineCapturePipeline(cfg.EventHandler, h)
	cfg.AgentEvents = combineCaptureAgent(cfg.AgentEvents, h)
	cfg.LLMTrace = combineCaptureTrace(cfg.LLMTrace, h)

	return &captureState{
		handler:      h,
		artifactBase: cfg.ArtifactDir,
		costLimited:  ResolveBudgetLimits(cfg.Budget, graph).MaxCostCents > 0,
		spec: pipeline.SpecArtifacts{
			SourcePath:     cfg.Capture.SourcePath,
			Source:         cfg.Capture.Source,
			Workflow:       cfg.Capture.Workflow,
			Params:         cfg.Params,
			BundleIdentity: cfg.BundleIdentity,
		},
	}
}

// combineCapturePipeline fans the pipeline-event stream to the caller's handler
// (if any) and the capture handler.
func combineCapturePipeline(existing pipeline.PipelineEventHandler, capture pipeline.PipelineEventHandler) pipeline.PipelineEventHandler {
	if existing == nil {
		return capture
	}
	return pipeline.PipelineMultiHandler(existing, capture)
}

// combineCaptureAgent fans agent events to the caller's handler (if any) and
// the capture handler's WriteAgentEvent.
func combineCaptureAgent(existing agent.EventHandler, h *pipeline.JSONLEventHandler) agent.EventHandler {
	if existing == nil {
		return agent.EventHandlerFunc(h.WriteAgentEvent)
	}
	return agent.EventHandlerFunc(func(evt agent.Event) {
		existing.HandleEvent(evt)
		h.WriteAgentEvent(evt)
	})
}

// combineCaptureTrace fans the raw LLM trace to the caller's observer (if any)
// and the capture handler's observer. The capture side goes through
// LLMTraceObserver, which drops SessionOwned events so a session's calls are
// not logged twice (once as agent llm_* events, once here).
func combineCaptureTrace(existing llm.TraceObserver, h *pipeline.JSONLEventHandler) llm.TraceObserver {
	obs := h.LLMTraceObserver()
	if existing == nil {
		return obs
	}
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		existing.HandleTraceEvent(evt)
		obs(evt)
	})
}

// fillCaptureFromSource returns a CaptureConfig with Source and Workflow filled
// from the parsed source when the caller left them empty. Used on the
// Run/NewEngineWithContext path, where the library holds the source string, so
// an embedder gets the same spec artifacts the CLI records at load time. DOT
// sources have no dippin IR, so they are left as-is. Parse failures leave
// Workflow nil — capture is best-effort telemetry and the run is validated
// separately.
func fillCaptureFromSource(cc *CaptureConfig, source, format string) *CaptureConfig {
	if format == "" {
		format = detectSourceFormat(source)
	}
	if format != "dip" {
		return cc
	}
	out := *cc
	if out.Source == "" {
		out.Source = source
	}
	if out.Workflow == nil {
		out.Workflow = parseWorkflowForCapture(source)
	}
	return &out
}

// parseWorkflowForCapture parses source into expanded IR for spec capture,
// mirroring loadDippinPipeline in the CLI. Returns nil on any parse or
// directive-resolution failure — best-effort telemetry never blocks a run.
func parseWorkflowForCapture(source string) *ir.Workflow {
	workflow, perr := parser.NewParser(source, "inline.dip").Parse()
	if perr != nil {
		return nil
	}
	if rerr := parser.ResolveFileDirectives(workflow, "."); rerr != nil {
		return nil
	}
	return workflow
}

// finalizeCapture writes the spec artifacts and run.json for a finished run,
// mirroring the CLI's finalizeRunCapture. Called from Engine.Run once the run
// ID exists (the first point it does) and while the capture handler is still
// open, so the manifest reads a fully written activity log. Failures warn and
// continue: losing telemetry is not worth failing a run that already did its
// work, and a missing manifest can be rebuilt later from the run dir.
func (e *Engine) finalizeCapture(runID string) {
	if e.capture == nil || runID == "" {
		return
	}
	runDir := filepath.Join(e.capture.artifactBase, runID)
	if _, err := os.Stat(runDir); err != nil {
		// No run directory means the run never wrote artifacts (a pre-execution
		// failure). Nothing to describe.
		return
	}
	spec := e.capture.spec
	if spec.Source != "" || spec.Workflow != nil {
		if _, err := pipeline.WriteSpecArtifacts(runDir, spec); err != nil {
			diag.Warnf("warning: spec capture failed: %v", err)
		}
	}
	m, err := pipeline.WriteRunManifest(runDir, runID)
	if err != nil {
		diag.Warnf("warning: run manifest failed: %v", err)
		return
	}
	// Surface unpriced usage the --max-cost ceiling could not bound (#518). A
	// warning, never a halt: a genuinely-free local model legitimately costs $0.
	pipeline.WarnUnpricedBudget(m.Totals, e.capture.costLimited)
}

// closeCapture closes the capture handler, flushing the sentinel-stripped
// activity.jsonl snapshot to the run dir. No-op when capture is off.
func (e *Engine) closeCapture() {
	if e.capture == nil || e.capture.handler == nil {
		return
	}
	_ = e.capture.handler.Close()
}

// attachClientObservers wires the token tracker, and any Config.LLMTrace
// observer, onto whichever *llm.Client backs this run (the auto-created client
// or a *llm.Client supplied via Config.LLMClient). A non-*llm.Client completer
// carries no observable transport, so it is a no-op there.
func attachClientObservers(client *llm.Client, completer agent.Completer, tokenTracker *llm.TokenTracker, cfg Config) {
	c := client
	if c == nil {
		if lc, ok := completer.(*llm.Client); ok {
			c = lc
		}
	}
	if c == nil {
		return
	}
	// Guard the double-count foot-gun: if the caller supplied a *llm.Client that
	// already has this exact tracker attached (and passed the same tracker as
	// Config.TokenTracker), adding it again would count every token twice. Skip
	// the re-add when it is already present.
	if !c.HasMiddleware(tokenTracker) {
		c.AddMiddleware(tokenTracker)
	}
	if cfg.LLMTrace != nil {
		c.AddTraceObserver(cfg.LLMTrace)
	}
}
