// ABOUTME: Classifies a terminal run error into a human-first cause + remediation
// ABOUTME: so failure surfaces lead with the problem, not the plumbing (#492).
package tracker

import (
	"errors"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// FailureCause is a human-first explanation of why a run failed: an icon + title
// to lead with, the underlying cause, and the concrete next step. Renderers show
// Title first and keep node/stage context secondary — the opposite of the raw
// "handler error at node X" wrapper.
type FailureCause struct {
	Kind      string // "billing" | "auth" | "rate_limit" | "context_length" | "network" | "timeout" | "config" | "content_filter" | "generic"
	Icon      string
	Title     string // the headline — the most important, actionable thing
	Detail    string // the underlying cause (e.g. the provider message), may be ""
	NextSteps string // what the user should do, may be ""
}

// ClassifyFailure maps a terminal run error to a FailureCause. It unwraps the
// node/handler wrappers to find the real cause (a typed provider error, a
// billing signal, etc.) and returns a generic "Run failed" carrying the raw
// message when nothing more specific matches. err==nil yields the zero value.
func ClassifyFailure(err error) FailureCause {
	if err == nil {
		return FailureCause{}
	}
	// Billing/quota exhaustion — reuse the account-attributed help (provider, env
	// var, masked key, billing URL). Checked first: it's the highest-signal,
	// two-minute-fix cause and its message is otherwise buried.
	if help, ok := llm.BillingHelp(err); ok {
		// No Detail: the provider's raw error body is often a JSON blob, and the
		// help already carries the clean, account-attributed explanation.
		return FailureCause{
			Kind: "billing", Icon: "💳",
			Title:     "Billing / quota exhausted",
			NextSteps: help,
		}
	}
	if fc, ok := classifyTypedProviderError(err); ok {
		return fc
	}
	if fc, ok := classifyBreach(err); ok {
		return fc
	}
	return FailureCause{Kind: "generic", Icon: "✗", Title: "Run failed", Detail: cleanCause(err)}
}

// classifyTypedProviderError matches the typed llm provider errors that carry a
// clear, actionable remediation. Grouped into sub-helpers to keep each small.
func classifyTypedProviderError(err error) (FailureCause, bool) {
	if fc, ok := classifyAuthError(err); ok {
		return fc, true
	}
	if fc, ok := classifyTransientError(err); ok {
		return fc, true
	}
	return classifyRequestError(err)
}

func classifyAuthError(err error) (FailureCause, bool) {
	var authErr *llm.AuthenticationError
	if errors.As(err, &authErr) {
		return FailureCause{Kind: "auth", Icon: "🔑", Title: "Authentication failed",
			Detail:    authErr.Error(),
			NextSteps: "Check the provider API key that's set (or run `tracker doctor` to see which keys are configured)."}, true
	}
	var accessErr *llm.AccessDeniedError
	if errors.As(err, &accessErr) {
		return FailureCause{Kind: "auth", Icon: "🔑", Title: "Access denied",
			Detail:    accessErr.Error(),
			NextSteps: "The key is valid but lacks access to this model/endpoint — check the account's model permissions."}, true
	}
	return FailureCause{}, false
}

func classifyTransientError(err error) (FailureCause, bool) {
	var rateErr *llm.RateLimitError
	if errors.As(err, &rateErr) {
		return FailureCause{Kind: "rate_limit", Icon: "⏳", Title: "Rate limited by the provider",
			Detail:    rateErr.Error(),
			NextSteps: "Transient throttling that outlasted the automatic retries — re-run (resume from the checkpoint), or lower concurrency."}, true
	}
	var netErr *llm.NetworkError
	if errors.As(err, &netErr) {
		return FailureCause{Kind: "network", Icon: "🌐", Title: "Network error reaching the provider",
			Detail:    netErr.Error(),
			NextSteps: "Check connectivity / any proxy or gateway URL, then re-run (resume from the checkpoint)."}, true
	}
	var toErr *llm.RequestTimeoutError
	if errors.As(err, &toErr) {
		return FailureCause{Kind: "timeout", Icon: "⏱️", Title: "Provider request timed out",
			Detail:    toErr.Error(),
			NextSteps: "Re-run (resume from the checkpoint); if it persists the provider or gateway may be degraded."}, true
	}
	return FailureCause{}, false
}

func classifyRequestError(err error) (FailureCause, bool) {
	var ctxLenErr *llm.ContextLengthError
	if errors.As(err, &ctxLenErr) {
		return FailureCause{Kind: "context_length", Icon: "📏", Title: "Context too long",
			Detail:    ctxLenErr.Error(),
			NextSteps: "The prompt exceeded the model's context window — shrink the input, split the node, or use a larger-context model."}, true
	}
	var cfgErr *llm.ConfigurationError
	if errors.As(err, &cfgErr) {
		return FailureCause{Kind: "config", Icon: "⚙️", Title: "Configuration problem",
			Detail:    cfgErr.Error(),
			NextSteps: "Run `tracker doctor` (or `tracker setup`) to configure a provider before running."}, true
	}
	var cfErr *llm.ContentFilterError
	if errors.As(err, &cfErr) {
		return FailureCause{Kind: "content_filter", Icon: "🛑", Title: "Blocked by the provider's content filter",
			Detail:    cfErr.Error(),
			NextSteps: "The provider refused the request content — adjust the prompt for the failing node."}, true
	}
	return FailureCause{}, false
}

// classifyBreach matches the engine's turn-limit / no-progress breach messages —
// not provider errors, but common node-level failures that deserve guidance
// rather than a bare "Run failed".
func classifyBreach(err error) (FailureCause, bool) {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "exhausted turn limit") || strings.Contains(msg, "turn limit"):
		return FailureCause{Kind: "turn_limit", Icon: "⏱️", Title: "Agent hit its turn limit",
			Detail:    cleanCause(err),
			NextSteps: "The agent ran out of turns before finishing. Raise the node's `max_turns:`, or break the task into smaller milestones so each fits the budget."}, true
	case strings.Contains(msg, "no-progress detected") || strings.Contains(msg, "no progress"):
		return FailureCause{Kind: "no_progress", Icon: "🔁", Title: "Agent stalled — no progress",
			Detail:    cleanCause(err),
			NextSteps: "The agent made no tool calls for several turns and looked stuck. Check the node's prompt/context for a blocker, or adjust `no_progress_turns:`."}, true
	}
	return FailureCause{}, false
}

// cleanCause strips the repeated node/handler wrapper prefixes so the message
// leads with the actual cause. Wrappers look like `handler error at node "X": `
// and `node "X": ` prepended by the engine and handler respectively.
func cleanCause(err error) string {
	msg := err.Error()
	for {
		trimmed := stripWrapperPrefix(msg)
		if trimmed == msg {
			return msg
		}
		msg = trimmed
	}
}

func stripWrapperPrefix(msg string) string {
	if rest, ok := stripIDWrapper(msg); ok {
		return rest
	}
	if rest, ok := stripPlainWrapper(msg); ok {
		return rest
	}
	return msg
}

// stripIDWrapper drops a `handler error at node "X": ` / `node "X": ` prefix.
func stripIDWrapper(msg string) (string, bool) {
	for _, p := range []string{"handler error at node ", "node "} {
		if strings.HasPrefix(msg, p) {
			if _, rest, found := strings.Cut(msg, `": `); found {
				return rest, true
			}
		}
	}
	return msg, false
}

// stripPlainWrapper drops a plain `<phrase>: ` prefix added by higher layers
// (e.g. the CLI's interpretRunResult).
func stripPlainWrapper(msg string) (string, bool) {
	for _, p := range []string{"pipeline execution: ", "pipeline finished with status: "} {
		if rest, found := strings.CutPrefix(msg, p); found {
			return rest, true
		}
	}
	return msg, false
}
