// ABOUTME: Derives a point-in-time ChatGPT sign-in summary (plan, refresh and expiry times) from
// ABOUTME: the token file, folding a missing file into a non-error "not signed in" result.
package chatgptauth

import (
	"errors"
	"fmt"
	"time"
)

// Status is a point-in-time summary of the ChatGPT sign-in, for `tracker
// setup`, `tracker doctor` and `tracker version` to display.
type Status struct {
	SignedIn                  bool
	PlanType                  string
	LastRefresh, AccessExpiry time.Time
}

// ReadStatus loads the token file at path and summarizes it. A missing file
// (ErrNotSignedIn) is not an error here — it is the ordinary not-signed-in
// state — and reports as a zero Status. Any other Load failure (an unsafe
// file, an incomplete sign-in) is returned as an error so the caller
// surfaces the real problem instead of silently reporting "not signed in".
func ReadStatus(path string) (Status, error) {
	t, err := Load(path)
	if err != nil {
		if errors.Is(err, ErrNotSignedIn) {
			return Status{}, nil
		}
		return Status{}, err
	}

	identity, err := ParseIDToken(t.IDToken)
	if err != nil {
		return Status{}, fmt.Errorf("parse chatgpt identity: %w", err)
	}
	expiry, _ := AccessTokenExpiry(t.AccessToken)
	return Status{
		SignedIn:     true,
		PlanType:     identity.PlanType,
		LastRefresh:  t.LastRefresh,
		AccessExpiry: expiry,
	}, nil
}
