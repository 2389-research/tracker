// ABOUTME: Provider/model failover — switch lanes on a billing/quota exhaustion so
// ABOUTME: one dead upstream account doesn't end a run when another lane is configured (#486).
package llm

import "context"

// Target is one provider+model lane for failover.
type Target struct {
	Provider string
	Model    string
}

// FailoverEvent describes a single lane switch, for the caller to surface in the
// audit trail (which target served which node).
type FailoverEvent struct {
	From Target // the exhausted lane
	To   Target // the lane being tried next
	Err  error  // the error that triggered the switch
}

// CompleteFailover tries req against its own provider/model first, then each
// fallback Target in order, switching lanes on a failover-class error. onFailover
// (optional) is called before each switch so the caller can emit an audit event.
//
// Only a *failover-class* error switches lanes (see isFailoverClass): a billing/
// quota exhaustion means the current account is out of credit, so another lane
// may succeed. A transient error was already retried by the retry middleware; a
// code/auth/context error would fail identically on every lane, so those are
// returned as-is without burning the other lanes.
func (c *Client) CompleteFailover(ctx context.Context, req *Request, fallbacks []Target, onFailover func(FailoverEvent)) (*Response, error) {
	resp, err := c.Complete(ctx, req)
	if err == nil || !isFailoverClass(err) {
		return resp, err
	}
	from := Target{Provider: req.Provider, Model: req.Model}
	for _, to := range fallbacks {
		if onFailover != nil {
			onFailover(FailoverEvent{From: from, To: to, Err: err})
		}
		next := cloneRequest(req)
		next.Provider = to.Provider
		next.Model = to.Model
		resp, err = c.Complete(ctx, next)
		if err == nil || !isFailoverClass(err) {
			return resp, err
		}
		from = to
	}
	// Every lane exhausted — return the last lane's error (still a billing class,
	// so an OutcomePausedBilling upstream still pauses rather than hard-fails).
	return resp, err
}

// isFailoverClass reports whether err is worth switching lanes for. Billing/quota
// exhaustion is the clear case: the current lane is out of credit and another may
// have budget. (Retryable throttling is handled by the retry middleware, and
// IsBillingError already excludes retryable rate limits.)
func isFailoverClass(err error) bool {
	return IsBillingError(err)
}
