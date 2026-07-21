// ABOUTME: The `tracker estimate` command — a rough pre-run cost/scale ballpark.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	tracker "github.com/2389-research/tracker"
)

// executeEstimate prints a rough pre-run cost/scale estimate for a pipeline.
func executeEstimate(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		return fmt.Errorf("usage: tracker estimate <pipeline.dip|name>")
	}
	resolved, isEmbedded, info, err := resolvePipelineSource(cfg.pipelineFile)
	if err != nil {
		return err
	}
	source, displayName, err := readPipelineSource(resolved, isEmbedded, info)
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}
	est, err := tracker.EstimateRun(context.Background(), source)
	if err != nil {
		return err
	}
	printEstimate(os.Stdout, displayName, est)
	return nil
}

func printEstimate(w io.Writer, name string, est *tracker.RunEstimate) {
	fmt.Fprintf(w, "Estimate — %s\n", name)
	fmt.Fprintf(w, "  Steps:       %d\n", est.Steps)
	fmt.Fprintf(w, "  Agent nodes: %d\n", est.AgentNodes)
	if len(est.Models) > 0 {
		fmt.Fprintf(w, "  Models:      %s\n", strings.Join(est.Models, ", "))
	}
	fmt.Fprintf(w, "  Cost:        $%.2f – $%.2f   (expected ~$%.2f)\n", est.LowUSD, est.HighUSD, est.ExpectedUSD)
	fmt.Fprintln(w, "  Rough estimate — actual cost depends on how many turns each agent uses and how many times loops run.")
}
