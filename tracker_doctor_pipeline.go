// ABOUTME: Doctor checks for pipeline files (.dip/.dot) and .dipx bundles.
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// checkPipelineFile parses and validates a pipeline file.
func checkPipelineFile(pipelineFile string) CheckResult {
	out := CheckResult{Name: "Pipeline File"}
	if _, err := os.Stat(pipelineFile); err != nil {
		return pipelineFileStatError(out, pipelineFile, err)
	}
	// .dipx bundles are ZIP archives produced by `dippin pack`, not text source.
	// dispatch through LoadDipxBundle so dipx.Open can verify the manifest and
	// every embedded workflow before we report success. parsePipelineSource
	// below would choke on the ZIP bytes if we fell through.
	if strings.EqualFold(filepath.Ext(pipelineFile), ".dipx") {
		return checkPipelineBundle(pipelineFile)
	}
	hasWarn := false
	if !strings.HasSuffix(pipelineFile, ".dip") && !strings.HasSuffix(pipelineFile, ".dot") {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: fmt.Sprintf("%s is not a .dip, .dot, or .dipx file — may not be a valid pipeline", pipelineFile),
		})
		hasWarn = true
	}
	fileBytes, err := os.ReadFile(pipelineFile)
	if err != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s: read error: %v", pipelineFile, err)
		out.Hint = "check file permissions"
		return out
	}
	graph, err := parsePipelineSource(string(fileBytes), detectSourceFormat(string(fileBytes)))
	if err != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s: parse error: %v", pipelineFile, err)
		out.Hint = "run `tracker validate " + pipelineFile + "` for full details"
		return out
	}
	return validatePipelineGraph(out, pipelineFile, graph, hasWarn)
}

// pipelineFileStatError maps an os.Stat failure on the pipeline file to an
// error result with path-specific guidance.
func pipelineFileStatError(out CheckResult, pipelineFile string, err error) CheckResult {
	out.Status = CheckStatusError
	switch {
	case os.IsNotExist(err):
		out.Message = fmt.Sprintf("%s does not exist", pipelineFile)
		out.Hint = fmt.Sprintf("check the file path: %s", pipelineFile)
	case os.IsPermission(err):
		out.Message = fmt.Sprintf("permission denied reading %s", pipelineFile)
		out.Hint = fmt.Sprintf("check permissions: chmod +r %s", pipelineFile)
	default:
		out.Message = fmt.Sprintf("cannot stat %s: %v", pipelineFile, err)
		out.Hint = "check the file path and permissions"
	}
	return out
}

// validatePipelineGraph runs tracker's semantic validation + lint on a parsed
// graph and renders the result (errors, warnings-only, or clean).
func validatePipelineGraph(out CheckResult, pipelineFile string, graph *pipeline.Graph, hasWarn bool) CheckResult {
	registry := buildDoctorValidationRegistry()
	ve := pipeline.ValidateAllWithLint(graph, registry)
	if ve != nil && len(ve.Errors) > 0 {
		return pipelineValidationErrors(out, pipelineFile, ve)
	}
	if ve != nil && len(ve.Warnings) > 0 {
		return pipelineValidationWarnings(out, pipelineFile, graph, ve)
	}
	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("%s valid (%d nodes, %d edges)", pipelineFile, len(graph.Nodes), len(graph.Edges)),
	})
	if hasWarn {
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("%s is valid but has warnings", pipelineFile)
	} else {
		out.Status = CheckStatusOK
		out.Message = fmt.Sprintf("%s is valid", pipelineFile)
	}
	return out
}

// pipelineValidationErrors renders the failed-validation result, listing every
// error and warning detail.
func pipelineValidationErrors(out CheckResult, pipelineFile string, ve *pipeline.ValidationError) CheckResult {
	for _, e := range ve.Errors {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("error: %s", e),
		})
	}
	for _, w := range ve.Warnings {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: w,
		})
	}
	out.Status = CheckStatusError
	out.Message = fmt.Sprintf("%s failed validation (%d error(s))", pipelineFile, len(ve.Errors))
	out.Hint = "run `tracker validate " + pipelineFile + "` for full details"
	return out
}

// pipelineValidationWarnings renders the warnings-only result for a graph that
// validated with no errors.
func pipelineValidationWarnings(out CheckResult, pipelineFile string, graph *pipeline.Graph, ve *pipeline.ValidationError) CheckResult {
	for _, w := range ve.Warnings {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: w,
		})
	}
	out.Details = append(out.Details, CheckDetail{
		Status: CheckStatusOK,
		Message: fmt.Sprintf("%s valid (%d nodes, %d edges, %d warning(s))",
			pipelineFile, len(graph.Nodes), len(graph.Edges), len(ve.Warnings)),
	})
	out.Status = CheckStatusWarn
	out.Message = fmt.Sprintf("%s valid with %d warning(s)", pipelineFile, len(ve.Warnings))
	return out
}

// checkPipelineBundle handles the .dipx branch of checkPipelineFile. It loads
// the bundle via pipeline.LoadDipxBundle, which verifies SHA-256 hashes and
// converts every embedded workflow. A successful load is sufficient evidence
// that the bundle parses and has a valid shape — doctor does not need to run
// the pipeline.
func checkPipelineBundle(bundlePath string) CheckResult {
	out := CheckResult{Name: "Pipeline File"}
	entry, subgraphs, info, diags, err := pipeline.LoadDipxBundle(context.Background(), bundlePath)
	if err != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s: bundle load failed: %v", bundlePath, err)
		out.Hint = "run `tracker validate " + bundlePath + "` for full details"
		return out
	}
	for _, d := range diags {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: d.String(),
		})
	}
	// Run tracker's semantic validation + lint on the bundled entry graph
	// so .dipx gets the same coverage as the .dip path in checkPipelineFile.
	// dipx.Open + LoadDippinWorkflowFromIR already covered structural
	// validation; this layer adds tracker's handler-aware checks.
	registry := buildDoctorValidationRegistry()
	tracerWarnings, failed := appendBundleValidation(&out, bundlePath, pipeline.ValidateAllWithLint(entry, registry))
	if failed {
		return out
	}
	totalWarnings := len(diags) + tracerWarnings
	out.Details = append(out.Details, CheckDetail{
		Status: CheckStatusOK,
		Message: fmt.Sprintf("%s valid (%d nodes, %d edges, %d subgraph(s), identity %s)",
			bundlePath, len(entry.Nodes), len(entry.Edges), len(subgraphs), info.Identity),
	})
	if totalWarnings > 0 {
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("%s valid with %d warning(s)", bundlePath, totalWarnings)
	} else {
		out.Status = CheckStatusOK
		out.Message = fmt.Sprintf("%s is valid", bundlePath)
	}
	return out
}

// appendBundleValidation renders tracker-side validation diagnostics onto the
// bundle result. It returns the warning count (when clean) and whether errors
// were found — in which case the caller returns out immediately.
func appendBundleValidation(out *CheckResult, bundlePath string, ve *pipeline.ValidationError) (tracerWarnings int, failed bool) {
	if ve == nil {
		return 0, false
	}
	for _, e := range ve.Errors {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("error: %s", e),
		})
	}
	for _, w := range ve.Warnings {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: w,
		})
	}
	if len(ve.Errors) > 0 {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s failed validation (%d error(s))", bundlePath, len(ve.Errors))
		out.Hint = "run `tracker validate " + bundlePath + "` for full details"
		return 0, true
	}
	return len(ve.Warnings), false
}

// buildDoctorValidationRegistry creates a handler registry stocked with
// every known handler name. Used for pipeline validation without actually
// executing any handlers.
func buildDoctorValidationRegistry() *pipeline.HandlerRegistry {
	registry := pipeline.NewHandlerRegistry()
	names := []string{
		"codergen", "tool", "subgraph", "spawn",
		"start", "exit", "conditional",
		"wait.human", "parallel", "parallel.fan_in", "manager_loop",
	}
	for _, name := range names {
		registry.Register(&doctorMockHandler{name: name})
	}
	return registry
}

type doctorMockHandler struct{ name string }

func (h *doctorMockHandler) Name() string { return h.name }

func (h *doctorMockHandler) Execute(_ context.Context, _ *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
