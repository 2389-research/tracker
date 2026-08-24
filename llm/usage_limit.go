// ABOUTME: Detects a Claude subscription usage-limit (Max/Team/Pro rolling cap)
// ABOUTME: — a recoverable "resets at X" pause, distinct from credit/quota exhaustion.
package llm

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// usageLimitSignals mark a Claude subscription usage-limit — a rolling/weekly
// plan cap ("you've reached your usage limit, resets at …") that clears on a
// schedule. The discriminator is the phrase "usage limit": a plain per-minute
// rate limit says "rate limit"/"too many requests", and credit/quota exhaustion
// (IsBillingError) says "credit balance"/"insufficient_quota"/"quota exceeded" —
// none of which contain it.
var usageLimitSignals = []string{
	"usage limit",
}

// IsUsageLimit reports whether err is a subscription usage-limit signal — the
// RECOVERABLE "you've hit your plan's cap, it resets at X" condition. It is a
// companion to IsBillingError, NOT an extension of it: usage-limit is
// deliberately kept out of IsBillingError so it is never mistaken for
// insufficient_quota/credit-balance, and — more importantly — so the codergen
// handler can route it to the resumable OutcomePausedBilling pause instead of
// letting a retryable-429-shaped failure reach the retry middleware, which would
// just re-hit the same cap. Returns false for insufficient_quota/credit-balance
// (that is IsBillingError) and for a plain retryable 429 rate limit.
func IsUsageLimit(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range usageLimitSignals {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// resetTimestampRe matchers extract the reset/resume-after time a usage-limit
// message carries, so a scheduler can hold a paused run until the subscription
// resets rather than relaunching straight into the same cap (#591).
var (
	// rfc3339ResetRe matches an embedded RFC3339 timestamp (optional fractional
	// seconds; Z or ±hh:mm offset), e.g. "usage limit reached, resets at
	// 2026-08-24T15:00:00Z".
	rfc3339ResetRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	// epochResetRe matches a bare 10-digit Unix epoch (seconds) in the 2001–2033
	// range — the form the Claude CLI emits after a pipe, e.g.
	// "Claude AI usage limit reached|1755990000".
	epochResetRe = regexp.MustCompile(`\b1\d{9}\b`)
)

// UsageLimitResetAt extracts the reset time embedded in a usage-limit error's
// message, returning (zero, false) when none is discoverable. A missing reset is
// not an error — the pause still happens, just without a scheduled resume hint
// (PauseError.ResumeAfter stays the zero value = unknown). Recognized forms: an
// RFC3339 timestamp ("resets at 2026-08-24T15:00:00Z") and a Unix-epoch seconds
// value (the Claude CLI's "usage limit reached|<epoch>").
func UsageLimitResetAt(err error) (time.Time, bool) {
	if err == nil {
		return time.Time{}, false
	}
	msg := err.Error()
	if m := rfc3339ResetRe.FindString(msg); m != "" {
		if t, perr := time.Parse(time.RFC3339, m); perr == nil {
			return t, true
		}
	}
	if m := epochResetRe.FindString(msg); m != "" {
		if secs, perr := strconv.ParseInt(m, 10, 64); perr == nil {
			return time.Unix(secs, 0).UTC(), true
		}
	}
	return time.Time{}, false
}
