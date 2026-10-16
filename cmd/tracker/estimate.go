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
	// Load only the operator's config .env, as `tracker version` does: the
	// TRACKER_OPENAI_AUTH switch `tracker setup` writes there must be visible
	// to pricing, but estimate is run casually inside untrusted checkouts and
	// must never pick up a project .env from the current directory (#659).
	if err := loadConfigEnvOnly(cfg.envFiles); err != nil {
		return err
	}
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
	est, err := tracker.EstimateRun(context.Background(), source, tracker.WithSource(sourceRef(resolved, isEmbedded, info)))
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
	if est.SubscriptionNodes > 0 {
		fmt.Fprintf(w, "  Subscription: %d openai node(s) on your ChatGPT subscription — $0 here; dollar ceilings don't count them.\n", est.SubscriptionNodes)
	}
	fmt.Fprintln(w, "  Rough estimate — actual cost depends on how many turns each agent uses and how many times loops run.")
}
