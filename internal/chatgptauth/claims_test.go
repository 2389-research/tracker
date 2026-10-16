// ABOUTME: Tests for decoding ChatGPT identity and access-token expiry from JWT claims — no
// ABOUTME: signature check, matching Codex (spec §1 "Identity").
package chatgptauth

import (
	"testing"
	"time"
)

func TestParseIDTokenReadsAccountAndPlan(t *testing.T) {
	token := fakeJWT(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-1",
			"chatgpt_plan_type":  "plus",
		},
	})

	got, err := ParseIDToken(token)
	if err != nil {
		t.Fatalf("ParseIDToken() unexpected error: %v", err)
	}
	want := Identity{AccountID: "acct-1", PlanType: "plus"}
	if got != want {
		t.Errorf("ParseIDToken() = %+v, want %+v", got, want)
	}
}

func TestParseIDTokenRejectsMalformed(t *testing.T) {
	for _, tok := range []string{"a.b", "", "x..y"} {
		if _, err := ParseIDToken(tok); err == nil {
			t.Errorf("ParseIDToken(%q) = nil error, want an error", tok)
		}
	}
}

func TestAccessTokenExpiry(t *testing.T) {
	t.Run("exp present", func(t *testing.T) {
		token := fakeJWT(map[string]any{"exp": 1760000000})

		got, ok := AccessTokenExpiry(token)
		if !ok {
			t.Fatalf("AccessTokenExpiry() ok = false, want true")
		}
		want := time.Unix(1760000000, 0)
		if !got.Equal(want) {
			t.Errorf("AccessTokenExpiry() = %v, want %v", got, want)
		}
	})

	t.Run("exp absent", func(t *testing.T) {
		token := fakeJWT(map[string]any{"sub": "user-1"})

		if _, ok := AccessTokenExpiry(token); ok {
			t.Errorf("AccessTokenExpiry() ok = true, want false (no exp claim)")
		}
	})
}
