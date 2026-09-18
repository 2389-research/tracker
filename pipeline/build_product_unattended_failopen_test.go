// ABOUTME: Fresh-eyes follow-up to #640 A2 on the REAL build_product.dip graph: under --auto-approve an
// ABOUTME: AGENT failure (red/unverified/absent build) must end the run `fail`, never ship via a gate's default.
package pipeline

import (
	"testing"
)

// bpUnattendedFailCase is one agent/verification failure that, pre-split, was
// routed to EscalateReview — whose unattended default is `accept` (override:
// true) -> Cleanup -> FinalCommit -> Done, ending the run validation_overridden
// with a red, unreviewed or EMPTY build behind it.
type bpUnattendedFailCase struct {
	name string
	// script overrides on top of the one-milestone happy path.
	script map[string]func(int) Outcome
	// want is where the failure must land: AbortRun (pre-build: nothing to
	// review) or EscalateVerification (post-build: red/unverified). "" means
	// the engine dead-stops the run with no route at all (still `fail`).
	want string
}

// bpRetry is an agent that keeps returning OutcomeRetry (transient provider
// errors): the engine re-runs it max_retries times and then takes the node's
// fallback_target (graph-level on_failure when it has none) — the only way a
// route other than the success/fail edges is taken. The engine coerces every
// other non-success status to `fail` (handleOutcomeStatus), so the
// unconditional catch-all edges (`SynthesizeReviews -> EscalateVerification`,
// `ApplyReviewFixes -> EscalateVerification`, ...) are reached exactly when
// ctx.outcome = fail misses a conditional — i.e. on a node like
// ApplyReviewFixes whose only conditional edge is the success one.
func bpRetry() Outcome {
	return Outcome{Status: OutcomeRetry, FailureReason: "sim: transient provider error"}
}

func bpUnattendedFailCases() []bpUnattendedFailCase {
	return []bpUnattendedFailCase{
		{name: "ReadSpec fail", want: "AbortRun", script: map[string]func(int) Outcome{
			"ReadSpec": func(int) Outcome { return bpFail("") },
		}},
		{name: "Decompose fail", want: "AbortRun", script: map[string]func(int) Outcome{
			"Decompose": func(int) Outcome { return bpFail("") },
		}},
		{name: "ReviewParallel fail", want: "EscalateVerification", script: map[string]func(int) Outcome{
			"ReviewParallel": func(int) Outcome { return bpFail("") },
		}},
		{name: "CheckReviewsComplete fail", want: "EscalateVerification", script: map[string]func(int) Outcome{
			"CheckReviewsComplete": func(int) Outcome { return bpFail("ERROR: review-gemini.md missing") },
		}},
		// SynthesizeReviews' success/fail edges are exhaustive and it has no
		// node-level fallback_target; retries exhausted therefore dead-stop
		// the run `fail` (handleRetryExhausted reads only the node attr, not
		// the graph-level on_failure) — nothing shipped either way.
		{name: "SynthesizeReviews retries exhausted", want: "", script: map[string]func(int) Outcome{
			"SynthesizeReviews": func(int) Outcome { return bpRetry() },
		}},
		// ApplyReviewFixes fail: no fail edge, so the unconditional catch-all
		// edge is the route (the strict-failure rule lets a node with ANY
		// conditional edge fall through to its unconditional one).
		{name: "ApplyReviewFixes fail (catch-all edge)", want: "EscalateVerification", script: map[string]func(int) Outcome{
			"SynthesizeReviews": func(int) Outcome { return bpFail("must-fix findings") },
			"ApplyReviewFixes":  func(int) Outcome { return bpFail("") },
		}},
		{name: "ApplyReviewFixes retries exhausted (fallback_target)", want: "EscalateVerification", script: map[string]func(int) Outcome{
			"SynthesizeReviews": func(int) Outcome { return bpFail("must-fix findings") },
			"ApplyReviewFixes":  func(int) Outcome { return bpRetry() },
		}},
		{name: "FinalSpecCheck retries exhausted (fallback_target)", want: "EscalateVerification", script: map[string]func(int) Outcome{
			"FinalSpecCheck": func(int) Outcome { return bpRetry() },
		}},
		{name: "FinalBuild fail", want: "EscalateVerification", script: map[string]func(int) Outcome{
			"FinalBuild": func(int) Outcome { return bpFail("go test ./... FAIL") },
		}},
		{name: "FinalSpecCheck fail", want: "EscalateVerification", script: map[string]func(int) Outcome{
			"FinalSpecCheck": func(int) Outcome { return bpFail("") },
		}},
	}
}

// TestBuildProductUnattendedAgentFailuresNeverShip drives each pre-build and
// post-build agent/verification failure on the real graph with every human
// gate answering exactly what --auto-approve answers (the gate's declared
// `default:`), and proves the run ends `fail`: Cleanup / FinalCommit / Done
// never run and no validation_overridden is recorded. Pre-build failures
// abort (nothing to review); post-build failures reach EscalateVerification
// whose unattended default is `abandon` -> the same AbortRun fail terminal.
func TestBuildProductUnattendedAgentFailuresNeverShip(t *testing.T) {
	g := loadBuildProduct(t)
	// The retry cases exhaust max_retries; the "none" policy skips the 2s
	// exponential backoff (max_retries from the .dip defaults still applies).
	g.Attrs["default_retry_policy"] = "none"
	for _, tc := range bpUnattendedFailCases() {
		t.Run(tc.name, func(t *testing.T) {
			script := map[string]func(int) Outcome{"PickNextMilestone": oneMilestone()}
			for k, v := range tc.script {
				script[k] = v
			}
			sim := &bp640Sim{script: script, gate: bpGateAutoApprove}
			res, err := sim.run(t, g)
			if tc.want != "" && !sim.visited(tc.want) {
				t.Fatalf("failure never reached %s: err=%v status=%s visits=%v", tc.want, err, statusOf(res), sim.visits)
			}
			if res == nil || res.Status != OutcomeFail {
				t.Fatalf("unattended run must end fail (not success / validation_overridden): err=%v status=%s visits=%v", err, statusOf(res), sim.visits)
			}
			for _, shipped := range []string{"Cleanup", "FinalCommit", "Done", "EscalateReview"} {
				if sim.visited(shipped) {
					t.Errorf("%s ran after %s — an unverified build shipped under --auto-approve: visits=%v", shipped, tc.name, sim.visits)
				}
			}
			if len(res.ValidationOverrides) != 0 {
				t.Errorf("an unattended abandon must not record an override: %+v", res.ValidationOverrides)
			}
			// Both routed cases halt exactly once at the AbortRun terminal
			// (its self-fallback is a no-op, #650).
			if last := sim.visits[len(sim.visits)-1]; tc.want != "" && (last != "AbortRun" || sim.count("AbortRun") != 1) {
				t.Errorf("run must halt once at AbortRun, last visited = %s, AbortRun ran %d times (visits=%v)", last, sim.count("AbortRun"), sim.visits)
			}
		})
	}
}

// TestBuildProductEscalateVerificationGateShape pins the split gate itself:
// freeform, `default: abandon` (what --auto-approve / a timeout picks, #646),
// abandon listed FIRST (labels[0] agrees with the default), the AbortRun fail
// terminal behind abandon, retry -> ResetReviewBudget, and accept an audited
// override.
func TestBuildProductEscalateVerificationGateShape(t *testing.T) {
	g := loadBuildProduct(t)
	n, ok := g.Nodes["EscalateVerification"]
	if !ok {
		t.Fatal("EscalateVerification gate missing from build_product.dip")
	}
	if n.Handler != "wait.human" || n.Attrs["mode"] != "freeform" {
		t.Errorf("EscalateVerification handler/mode = %q/%q, want wait.human/freeform", n.Handler, n.Attrs["mode"])
	}
	if def := n.HumanConfig().DefaultChoice; def != "abandon" {
		t.Errorf("EscalateVerification default = %q, want abandon — the unattended default must not ship an unverified build", def)
	}
	labels := collectLabels(g, "EscalateVerification")
	if len(labels) != 3 || labels[0] != "abandon" {
		t.Errorf("EscalateVerification labels = %v, want abandon FIRST then retry/accept (labels[0] is the freeform fallback)", labels)
	}
	if !hasLabeledEdgeTo(g, "EscalateVerification", "AbortRun", "abandon") {
		t.Error("EscalateVerification abandon must route to the AbortRun fail terminal (a bare -> Done ends the run success)")
	}
	if !hasLabeledEdgeTo(g, "EscalateVerification", "ResetReviewBudget", "retry") {
		t.Error("EscalateVerification retry must route through ResetReviewBudget -> Decompose (re-plan)")
	}
	var accept *Edge
	for _, e := range g.OutgoingEdges("EscalateVerification") {
		if e.Label == "accept" {
			accept = e
		}
	}
	if accept == nil || accept.To != "Cleanup" || !accept.Override {
		t.Errorf("EscalateVerification accept must be `-> Cleanup override: true` (an audited override), got %+v", accept)
	}
	// The accept-default gate keeps exactly ONE non-fallback entry: the
	// exhausted re-review budget (green tree, reviewers still object).
	for _, e := range g.Edges {
		if e.To != "EscalateReview" {
			continue
		}
		if e.From != "CheckReviewFixBudget" {
			t.Errorf("edge %s -> EscalateReview [%s]: only CheckReviewFixBudget may reach the accept-default gate; red/unverified/pre-build failures belong on EscalateVerification / AbortRun", e.From, e.Condition)
		}
	}
	if !hasEdgeWithCondition(g, "CheckReviewFixBudget", "EscalateReview", "ctx.outcome = fail") {
		t.Error("CheckReviewFixBudget exhausted must still reach EscalateReview (documented accept-default behaviour)")
	}
	// The unconditional catch-all edges of the post-build agent/verification
	// nodes must all land on the abandon-default gate as well.
	for _, from := range []string{"SynthesizeReviews", "ApplyReviewFixes", "FinalBuild", "FinalSpecCheck"} {
		if !hasUnconditionalEdgeTo(g, from, "EscalateVerification") {
			t.Errorf("%s has no unconditional catch-all edge to EscalateVerification: edges=%v", from, describeEdges(g, from))
		}
	}
	for _, from := range []string{"ReviewParallel", "CheckReviewsComplete", "FinalBuild", "FinalSpecCheck"} {
		if !hasEdgeWithCondition(g, from, "EscalateVerification", "ctx.outcome = fail") {
			t.Errorf("%s has no `ctx.outcome = fail -> EscalateVerification` edge: edges=%v", from, describeEdges(g, from))
		}
	}
	if fb := resolveFallback(g, g.Nodes["FinalSpecCheck"]); fb != "EscalateVerification" {
		t.Errorf("FinalSpecCheck fallback = %q, want EscalateVerification (a failed compliance verdict is unverified)", fb)
	}
}

// TestBuildProductReviewBudgetExhaustedStillAccepts pins the documented
// behaviour the split deliberately preserves: SynthesizeReviews finds must-fix
// findings, the fix pass succeeds, the re-review budget is exhausted, and the
// unattended EscalateReview default `accept` ships the (green) build as an
// audited validation_overridden.
func TestBuildProductReviewBudgetExhaustedStillAccepts(t *testing.T) {
	g := loadBuildProduct(t)
	sim := &bp640Sim{gate: bpGateAutoApprove, script: map[string]func(int) Outcome{
		"PickNextMilestone":    oneMilestone(),
		"SynthesizeReviews":    func(int) Outcome { return bpFail("must-fix findings") },
		"CheckReviewFixBudget": func(int) Outcome { return bpFail("review-fix budget exhausted") },
	}}
	res, err := sim.run(t, g)
	if err != nil || res == nil {
		t.Fatalf("run errored: %v visits=%v", err, sim.visits)
	}
	for _, id := range []string{"ApplyReviewFixes", "CheckReviewFixBudget", "EscalateReview", "Cleanup", "FinalCommit", "Done"} {
		if !sim.visited(id) {
			t.Errorf("%s never ran on the exhausted-budget accept path: visits=%v", id, sim.visits)
		}
	}
	if sim.visited("EscalateVerification") || sim.visited("AbortRun") {
		t.Errorf("exhausted re-review budget must not reach the abandon-default path: visits=%v", sim.visits)
	}
	if res.Status != OutcomeValidationOverridden {
		t.Errorf("status = %q, want %q — an unattended accept over exhausted re-review must stay an audited override", res.Status, OutcomeValidationOverridden)
	}
}

// TestBuildProductEscalateVerificationAcceptIsAudited: a HUMAN choosing accept
// at the verification gate ships, but only as validation_overridden.
func TestBuildProductEscalateVerificationAcceptIsAudited(t *testing.T) {
	g := loadBuildProduct(t)
	sim := &bp640Sim{gate: "accept", script: map[string]func(int) Outcome{
		"PickNextMilestone": oneMilestone(),
		"FinalBuild":        func(int) Outcome { return bpFail("go test ./... FAIL") },
	}}
	res, err := sim.run(t, g)
	if err != nil || res == nil {
		t.Fatalf("run errored: %v visits=%v", err, sim.visits)
	}
	if !sim.visited("EscalateVerification") || !sim.visited("FinalCommit") {
		t.Fatalf("explicit accept must ship through EscalateVerification -> Cleanup -> FinalCommit: visits=%v", sim.visits)
	}
	if res.Status != OutcomeValidationOverridden {
		t.Errorf("status = %q, want %q — accepting a red FinalBuild must be an audited override", res.Status, OutcomeValidationOverridden)
	}
}

func collectLabels(g *Graph, from string) []string {
	var out []string
	for _, e := range g.OutgoingEdges(from) {
		if e.Label != "" {
			out = append(out, e.Label)
		}
	}
	return out
}

// TestBuildProductEveryGateAbandonEndsFail drives each of the four human
// gates' `abandon` on the real graph and proves it ends the run `fail` at the
// AbortRun terminal with nothing shipped. Pre-fix, OperatorDecision /
// EscalateMilestone / EscalateReview routed abandon straight to Done, which
// ends the run `success` (the gate's own outcome is success and Done is the
// exit) — an abandoned build reported as shipped.
func TestBuildProductEveryGateAbandonEndsFail(t *testing.T) {
	g := loadBuildProduct(t)
	cases := []struct {
		gate   string
		script map[string]func(int) Outcome
	}{
		{gate: "OperatorDecision", script: map[string]func(int) Outcome{
			"Implement": func(int) Outcome {
				o := bpFail("turn limit")
				o.ContextUpdates["turn_breach_class"] = "operator_decision"
				return o
			},
		}},
		{gate: "EscalateMilestone", script: map[string]func(int) Outcome{
			"Implement": func(int) Outcome { return bpFail("") },
		}},
		{gate: "EscalateVerification", script: map[string]func(int) Outcome{
			"FinalBuild": func(int) Outcome { return bpFail("go test ./... FAIL") },
		}},
		{gate: "EscalateReview", script: map[string]func(int) Outcome{
			"SynthesizeReviews":    func(int) Outcome { return bpFail("must-fix findings") },
			"CheckReviewFixBudget": func(int) Outcome { return bpFail("review-fix budget exhausted") },
		}},
	}
	for _, tc := range cases {
		t.Run(tc.gate, func(t *testing.T) {
			if !hasLabeledEdgeTo(g, tc.gate, "AbortRun", "abandon") {
				t.Fatalf("%s has no abandon -> AbortRun edge", tc.gate)
			}
			script := map[string]func(int) Outcome{"PickNextMilestone": oneMilestone()}
			for k, v := range tc.script {
				script[k] = v
			}
			sim := &bp640Sim{script: script, gate: "abandon"}
			res, err := sim.run(t, g)
			if !sim.visited(tc.gate) {
				t.Fatalf("%s never reached: err=%v visits=%v", tc.gate, err, sim.visits)
			}
			if res == nil || res.Status != OutcomeFail {
				t.Fatalf("abandon at %s must end the run fail: err=%v status=%s visits=%v", tc.gate, err, statusOf(res), sim.visits)
			}
			if !sim.visited("AbortRun") || sim.visited("FinalCommit") || sim.visited("Done") {
				t.Errorf("abandon at %s must halt at AbortRun with nothing shipped: visits=%v", tc.gate, sim.visits)
			}
			if len(res.ValidationOverrides) != 0 {
				t.Errorf("abandon must not record an override: %+v", res.ValidationOverrides)
			}
		})
	}
}
