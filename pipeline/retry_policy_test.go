// ABOUTME: Tests for named retry policies including parsing, resolution, and backoff functions.
// ABOUTME: Covers table-driven tests for all named policies, backoff cap behavior, and node/graph attr resolution.
package pipeline

import (
	"testing"
	"time"
)

func TestParseRetryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		policyName string
		wantOK     bool
		wantMax    int
		wantBase   time.Duration
	}{
		{"none policy", "none", true, 0, 0},
		{"standard policy", "standard", true, 2, 2 * time.Second},
		{"aggressive policy", "aggressive", true, 5, 500 * time.Millisecond},
		{"patient policy", "patient", true, 3, 10 * time.Second},
		{"linear policy", "linear", true, 3, 2 * time.Second},
		{"unknown policy", "nonexistent", false, 0, 0},
		{"empty string", "", false, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, ok := ParseRetryPolicy(tt.policyName)
			if ok != tt.wantOK {
				t.Fatalf("ParseRetryPolicy(%q) ok = %v, want %v", tt.policyName, ok, tt.wantOK)
			}
			if !ok {
				if policy != nil {
					t.Fatalf("ParseRetryPolicy(%q) returned non-nil policy for unknown name", tt.policyName)
				}
				return
			}
			if policy.Name != tt.policyName {
				t.Errorf("policy.Name = %q, want %q", policy.Name, tt.policyName)
			}
			if policy.MaxRetries != tt.wantMax {
				t.Errorf("policy.MaxRetries = %d, want %d", policy.MaxRetries, tt.wantMax)
			}
			if policy.BaseDelay != tt.wantBase {
				t.Errorf("policy.BaseDelay = %v, want %v", policy.BaseDelay, tt.wantBase)
			}
			if policy.BackoffFn == nil {
				t.Error("policy.BackoffFn is nil")
			}
		})
	}
}

func TestResolveRetryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		nodeAttrs  map[string]string
		graphAttrs map[string]string
		wantPolicy string
		wantMax    int
	}{
		{
			name:       "default to standard when nothing set",
			nodeAttrs:  map[string]string{},
			graphAttrs: map[string]string{},
			wantPolicy: "standard",
			wantMax:    2,
		},
		{
			name:       "graph default_retry_policy overrides default",
			nodeAttrs:  map[string]string{},
			graphAttrs: map[string]string{"default_retry_policy": "aggressive"},
			wantPolicy: "aggressive",
			wantMax:    5,
		},
		{
			name:       "node retry_policy overrides graph default",
			nodeAttrs:  map[string]string{"retry_policy": "patient"},
			graphAttrs: map[string]string{"default_retry_policy": "aggressive"},
			wantPolicy: "patient",
			wantMax:    3,
		},
		{
			name:       "max_retries overrides policy max",
			nodeAttrs:  map[string]string{"retry_policy": "standard", "max_retries": "7"},
			graphAttrs: map[string]string{},
			wantPolicy: "standard",
			wantMax:    7,
		},
		{
			name:       "max_retries without explicit policy",
			nodeAttrs:  map[string]string{"max_retries": "4"},
			graphAttrs: map[string]string{},
			wantPolicy: "standard",
			wantMax:    4,
		},
		{
			name:       "unknown node policy falls back to standard",
			nodeAttrs:  map[string]string{"retry_policy": "bogus"},
			graphAttrs: map[string]string{},
			wantPolicy: "standard",
			wantMax:    2,
		},
		{
			name:       "unknown graph default falls back to standard",
			nodeAttrs:  map[string]string{},
			graphAttrs: map[string]string{"default_retry_policy": "bogus"},
			wantPolicy: "standard",
			wantMax:    2,
		},
		{
			name:       "none policy with max_retries override",
			nodeAttrs:  map[string]string{"retry_policy": "none", "max_retries": "1"},
			graphAttrs: map[string]string{},
			wantPolicy: "none",
			wantMax:    1,
		},
		{
			name:       "legacy default_max_retry graph attr as max override",
			nodeAttrs:  map[string]string{},
			graphAttrs: map[string]string{"default_max_retry": "6"},
			wantPolicy: "standard",
			wantMax:    6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &Node{
				ID:    "test_node",
				Attrs: tt.nodeAttrs,
			}
			policy := ResolveRetryPolicy(node, tt.graphAttrs)
			if policy.Name != tt.wantPolicy {
				t.Errorf("policy.Name = %q, want %q", policy.Name, tt.wantPolicy)
			}
			if policy.MaxRetries != tt.wantMax {
				t.Errorf("policy.MaxRetries = %d, want %d", policy.MaxRetries, tt.wantMax)
			}
		})
	}
}

func TestResolveRetryPolicyBaseDelay(t *testing.T) {
	tests := []struct {
		name      string
		nodeAttrs map[string]string
		wantDelay time.Duration
	}{
		{
			name:      "base_delay overrides policy default",
			nodeAttrs: map[string]string{"retry_policy": "standard", "base_delay": "500ms"},
			wantDelay: 500 * time.Millisecond,
		},
		{
			name:      "base_delay with seconds",
			nodeAttrs: map[string]string{"retry_policy": "aggressive", "base_delay": "5s"},
			wantDelay: 5 * time.Second,
		},
		{
			name:      "no base_delay uses policy default",
			nodeAttrs: map[string]string{"retry_policy": "standard"},
			wantDelay: 2 * time.Second,
		},
		{
			name:      "invalid base_delay ignored",
			nodeAttrs: map[string]string{"retry_policy": "standard", "base_delay": "not-a-duration"},
			wantDelay: 2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &Node{
				ID:    "test_node",
				Attrs: tt.nodeAttrs,
			}
			policy := ResolveRetryPolicy(node, map[string]string{})
			if policy.BaseDelay != tt.wantDelay {
				t.Errorf("policy.BaseDelay = %v, want %v", policy.BaseDelay, tt.wantDelay)
			}
		})
	}
}

func TestExponentialBackoff(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		base    time.Duration
		want    time.Duration // base (pre-jitter) expected value
	}{
		{"attempt 0", 0, 2 * time.Second, 2 * time.Second},
		{"attempt 1", 1, 2 * time.Second, 4 * time.Second},
		{"attempt 2", 2, 2 * time.Second, 8 * time.Second},
		{"attempt 3", 3, 2 * time.Second, 16 * time.Second},
		{"capped at 60s", 10, 2 * time.Second, 60 * time.Second},
		{"small base high attempt still caps", 5, 5 * time.Second, 60 * time.Second},
		{"attempt 0 with 500ms", 0, 500 * time.Millisecond, 500 * time.Millisecond},
		{"attempt 1 with 500ms", 1, 500 * time.Millisecond, 1 * time.Second},
		{"attempt 2 with 500ms", 2, 500 * time.Millisecond, 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExponentialBackoff(tt.attempt, tt.base)
			low := time.Duration(float64(tt.want) * 0.75)
			high := time.Duration(float64(tt.want) * 1.25)
			// Capped cases must not exceed maxBackoffDuration.
			if tt.want >= maxBackoffDuration {
				high = maxBackoffDuration
			}
			if got < low || got > high {
				t.Errorf("ExponentialBackoff(%d, %v) = %v, want within [%v, %v]", tt.attempt, tt.base, got, low, high)
			}
		})
	}
}

func TestLinearBackoff(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		base    time.Duration
		want    time.Duration // base (pre-jitter) expected value
	}{
		{"attempt 0", 0, 2 * time.Second, 2 * time.Second},
		{"attempt 1", 1, 2 * time.Second, 4 * time.Second},
		{"attempt 2", 2, 2 * time.Second, 6 * time.Second},
		{"attempt 3", 3, 2 * time.Second, 8 * time.Second},
		{"capped at 60s", 100, 2 * time.Second, 60 * time.Second},
		{"attempt 1 with 10s", 1, 10 * time.Second, 20 * time.Second},
		{"attempt 5 with 10s", 5, 10 * time.Second, 60 * time.Second},
		{"attempt 6 with 10s caps", 6, 10 * time.Second, 60 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinearBackoff(tt.attempt, tt.base)
			low := time.Duration(float64(tt.want) * 0.75)
			high := time.Duration(float64(tt.want) * 1.25)
			if tt.want >= maxBackoffDuration {
				high = maxBackoffDuration
			}
			if got < low || got > high {
				t.Errorf("LinearBackoff(%d, %v) = %v, want within [%v, %v]", tt.attempt, tt.base, got, low, high)
			}
		})
	}
}

func TestExponentialBackoffHasJitter(t *testing.T) {
	base := 2 * time.Second
	attempt := 2 // base delay = 8s
	expectedBase := 8 * time.Second
	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		d := ExponentialBackoff(attempt, base)
		seen[d] = true
		low := time.Duration(float64(expectedBase) * 0.75)
		high := time.Duration(float64(expectedBase) * 1.25)
		if d < low || d > high {
			t.Fatalf("backoff %v outside [%v, %v]", d, low, high)
		}
	}
	if len(seen) < 2 {
		t.Errorf("expected jitter variation, got %d unique values", len(seen))
	}
}

func TestLinearBackoffHasJitter(t *testing.T) {
	base := 2 * time.Second
	attempt := 2 // base delay = 6s
	expectedBase := 6 * time.Second
	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		d := LinearBackoff(attempt, base)
		seen[d] = true
		low := time.Duration(float64(expectedBase) * 0.75)
		high := time.Duration(float64(expectedBase) * 1.25)
		if d < low || d > high {
			t.Fatalf("backoff %v outside [%v, %v]", d, low, high)
		}
	}
	if len(seen) < 2 {
		t.Errorf("expected jitter variation, got %d unique values", len(seen))
	}
}
