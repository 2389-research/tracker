// ABOUTME: Named retry policies with configurable backoff strategies for pipeline nodes.
// ABOUTME: Provides resolution logic that checks node attrs, graph attrs, and falls back to defaults.
package pipeline

import (
	"strconv"
	"time"
)

const maxBackoffDuration = 60 * time.Second

// RetryPolicy defines a named retry strategy with configurable attempt count and backoff.
type RetryPolicy struct {
	Name       string
	MaxRetries int
	BaseDelay  time.Duration
	BackoffFn  func(attempt int, base time.Duration) time.Duration
}

// namedPolicies holds the built-in retry policy definitions.
var namedPolicies = map[string]func() *RetryPolicy{
	"none": func() *RetryPolicy {
		return &RetryPolicy{
			Name:       "none",
			MaxRetries: 0,
			BaseDelay:  0,
			BackoffFn:  ExponentialBackoff,
		}
	},
	"standard": func() *RetryPolicy {
		return &RetryPolicy{
			Name:       "standard",
			MaxRetries: 2,
			BaseDelay:  2 * time.Second,
			BackoffFn:  ExponentialBackoff,
		}
	},
	"aggressive": func() *RetryPolicy {
		return &RetryPolicy{
			Name:       "aggressive",
			MaxRetries: 5,
			BaseDelay:  500 * time.Millisecond,
			BackoffFn:  ExponentialBackoff,
		}
	},
	"patient": func() *RetryPolicy {
		return &RetryPolicy{
			Name:       "patient",
			MaxRetries: 3,
			BaseDelay:  10 * time.Second,
			BackoffFn:  ExponentialBackoff,
		}
	},
	"linear": func() *RetryPolicy {
		return &RetryPolicy{
			Name:       "linear",
			MaxRetries: 3,
			BaseDelay:  2 * time.Second,
			BackoffFn:  LinearBackoff,
		}
	},
}

// ParseRetryPolicy returns the named policy and true, or nil and false for unknown names.
func ParseRetryPolicy(name string) (*RetryPolicy, bool) {
	factory, ok := namedPolicies[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}

// ResolveRetryPolicy determines the retry policy for a node by checking:
// 1. Node attr "retry_policy"
// 2. Graph attr "default_retry_policy"
// 3. Falls back to "standard"
// The resolved policy's MaxRetries is then overridden by node attr "max_retries"
// or graph attr "default_max_retry" if either is set.
func ResolveRetryPolicy(node *Node, graphAttrs map[string]string) *RetryPolicy {
	var policy *RetryPolicy

	// Try node-level retry_policy attr first.
	if name, ok := node.Attrs["retry_policy"]; ok {
		policy, _ = ParseRetryPolicy(name)
	}

	// Try graph-level default_retry_policy if node didn't specify a valid one.
	if policy == nil {
		if name, ok := graphAttrs["default_retry_policy"]; ok {
			policy, _ = ParseRetryPolicy(name)
		}
	}

	// Fall back to standard.
	if policy == nil {
		policy, _ = ParseRetryPolicy("standard")
	}

	// Apply max_retries override from node attr.
	if mr, ok := node.Attrs["max_retries"]; ok {
		if n, err := strconv.Atoi(mr); err == nil {
			policy.MaxRetries = n
		}
	} else if mr, ok := graphAttrs["default_max_retry"]; ok {
		// Legacy graph-level max retry override.
		if n, err := strconv.Atoi(mr); err == nil {
			policy.MaxRetries = n
		}
	}

	// Apply base_delay override from node attr (set by Dippin adapter
	// from ir.RetryConfig.BaseDelay, e.g. "500ms", "2s").
	if bd, ok := node.Attrs["base_delay"]; ok {
		if d, err := time.ParseDuration(bd); err == nil {
			policy.BaseDelay = d
		}
	}

	return policy
}

// ExponentialBackoff returns 2^attempt * base, capped at 60s.
func ExponentialBackoff(attempt int, base time.Duration) time.Duration {
	delay := base
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > maxBackoffDuration {
			return maxBackoffDuration
		}
	}
	if delay > maxBackoffDuration {
		return maxBackoffDuration
	}
	return delay
}

// LinearBackoff returns (attempt+1) * base, capped at 60s.
// Like ExponentialBackoff, attempt is 0-indexed: attempt 0 = 1*base.
func LinearBackoff(attempt int, base time.Duration) time.Duration {
	delay := time.Duration(attempt+1) * base
	if delay > maxBackoffDuration {
		return maxBackoffDuration
	}
	return delay
}
