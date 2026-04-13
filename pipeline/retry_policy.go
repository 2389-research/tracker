// ABOUTME: Named retry policies with configurable backoff strategies for pipeline nodes.
// ABOUTME: Provides resolution logic that checks node attrs, graph attrs, and falls back to defaults.
package pipeline

import (
	"math/rand/v2"
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
	policy := resolveBaseRetryPolicy(node, graphAttrs)
	applyRetryOverrides(policy, node, graphAttrs)
	return policy
}

// resolveBaseRetryPolicy picks the policy by priority: node attr > graph default > standard.
func resolveBaseRetryPolicy(node *Node, graphAttrs map[string]string) *RetryPolicy {
	if name, ok := node.Attrs["retry_policy"]; ok {
		if p, found := ParseRetryPolicy(name); found {
			return p
		}
	}
	if name, ok := graphAttrs["default_retry_policy"]; ok {
		if p, found := ParseRetryPolicy(name); found {
			return p
		}
	}
	p, _ := ParseRetryPolicy("standard")
	return p
}

// applyRetryOverrides applies max_retries and base_delay overrides to the policy.
func applyRetryOverrides(policy *RetryPolicy, node *Node, graphAttrs map[string]string) {
	if mr := resolveMaxRetries(node.Attrs, graphAttrs); mr != "" {
		if n, err := strconv.Atoi(mr); err == nil {
			policy.MaxRetries = n
		}
	}
	if bd, ok := node.Attrs["base_delay"]; ok {
		if d, err := time.ParseDuration(bd); err == nil {
			policy.BaseDelay = d
		}
	}
}

// resolveMaxRetries returns the max retries string from node attrs, falling back to graph attrs.
func resolveMaxRetries(nodeAttrs, graphAttrs map[string]string) string {
	if mr := nodeAttrs["max_retries"]; mr != "" {
		return mr
	}
	return graphAttrs["default_max_retry"]
}

// applyJitter adds ±25% random jitter to a duration, capped at maxBackoffDuration.
func applyJitter(d time.Duration) time.Duration {
	jitter := 0.75 + rand.Float64()*0.5 // [0.75, 1.25)
	result := time.Duration(float64(d) * jitter)
	if result > maxBackoffDuration {
		return maxBackoffDuration
	}
	return result
}

// ExponentialBackoff returns 2^attempt * base with ±25% jitter, capped at 60s.
func ExponentialBackoff(attempt int, base time.Duration) time.Duration {
	delay := base
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > maxBackoffDuration {
			return applyJitter(maxBackoffDuration)
		}
	}
	if delay > maxBackoffDuration {
		return applyJitter(maxBackoffDuration)
	}
	return applyJitter(delay)
}

// LinearBackoff returns (attempt+1) * base with ±25% jitter, capped at 60s.
// Like ExponentialBackoff, attempt is 0-indexed: attempt 0 = 1*base.
func LinearBackoff(attempt int, base time.Duration) time.Duration {
	delay := time.Duration(attempt+1) * base
	if delay > maxBackoffDuration {
		return applyJitter(maxBackoffDuration)
	}
	return applyJitter(delay)
}
