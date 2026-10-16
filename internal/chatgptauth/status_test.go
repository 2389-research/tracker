// ABOUTME: Tests for ReadStatus: the signed-in summary derived from the token file, and the
// ABOUTME: not-signed-in case that must report as a zero Status rather than an error.
package chatgptauth

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadStatus(t *testing.T) {
	t.Run("signed in", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), FileName)
		idToken := fakeJWT(map[string]any{
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct-1",
				"chatgpt_plan_type":  "plus",
			},
		})
		accessToken := fakeJWT(map[string]any{"exp": 1760000000})
		wantExpiry := time.Unix(1760000000, 0)
		wantLastRefresh := time.Unix(1759990000, 0).UTC()

		if err := Save(path, Tokens{
			IDToken:      idToken,
			AccessToken:  accessToken,
			RefreshToken: "refresh-token",
			AccountID:    "acct-1",
			LastRefresh:  wantLastRefresh,
		}); err != nil {
			t.Fatalf("Save() unexpected error: %v", err)
		}

		got, err := ReadStatus(path)
		if err != nil {
			t.Fatalf("ReadStatus() unexpected error: %v", err)
		}
		if !got.SignedIn {
			t.Errorf("SignedIn = false, want true")
		}
		if got.PlanType != "plus" {
			t.Errorf("PlanType = %q, want %q", got.PlanType, "plus")
		}
		if !got.LastRefresh.Equal(wantLastRefresh) {
			t.Errorf("LastRefresh = %v, want %v", got.LastRefresh, wantLastRefresh)
		}
		if !got.AccessExpiry.Equal(wantExpiry) {
			t.Errorf("AccessExpiry = %v, want %v", got.AccessExpiry, wantExpiry)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), FileName)

		got, err := ReadStatus(path)
		if err != nil {
			t.Fatalf("ReadStatus() unexpected error: %v", err)
		}
		if got.SignedIn {
			t.Errorf("SignedIn = true, want false")
		}
		if got != (Status{}) {
			t.Errorf("ReadStatus() = %+v, want zero value Status{}", got)
		}
	})
}
