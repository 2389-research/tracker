// ABOUTME: Decodes ChatGPT identity (account ID, plan type) from an id_token's claims and reads
// ABOUTME: an access_token's exp claim — no signature check, matching Codex (spec §1 "Identity").
package chatgptauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Identity is the ChatGPT account identity carried in an id_token's
// namespaced auth claim.
type Identity struct{ AccountID, PlanType string }

// authClaimKey is the namespaced claim Codex's own id_token carries the
// ChatGPT account ID and plan type under (login/src/token_data.rs:77-98).
const authClaimKey = "https://api.openai.com/auth"

// ParseIDToken decodes idToken's claims and reads the account ID and plan
// type from its "https://api.openai.com/auth" claim. It does not check the
// token's signature, matching Codex's own id_token handling.
func ParseIDToken(idToken string) (Identity, error) {
	claims, err := decodeJWTClaims(idToken)
	if err != nil {
		return Identity{}, fmt.Errorf("parse id token: %w", err)
	}
	auth, ok := claims[authClaimKey].(map[string]any)
	if !ok {
		return Identity{}, fmt.Errorf("parse id token: missing %q claim", authClaimKey)
	}
	accountID, _ := auth["chatgpt_account_id"].(string)
	planType, _ := auth["chatgpt_plan_type"].(string)
	return Identity{AccountID: accountID, PlanType: planType}, nil
}

// AccessTokenExpiry decodes accessToken's claims and returns its exp claim as
// a time, or false when the token can't be decoded or carries no exp claim.
func AccessTokenExpiry(accessToken string) (time.Time, bool) {
	claims, err := decodeJWTClaims(accessToken)
	if err != nil {
		return time.Time{}, false
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(int64(exp), 0), true
}

// decodeJWTClaims splits token on ".", requires three non-empty parts, and
// decodes the middle (payload) part as JSON. It never checks the signature
// (the third part) — matching Codex, which trusts its own issued tokens
// without re-verifying them (login/src/token_data.rs:129-140).
func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed JWT: want three dot-separated parts, got %d", len(parts))
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("malformed JWT: a dot-separated part is empty")
	}

	// base64.RawURLEncoding expects no padding; trimming trailing "=" also
	// accepts a standards-compliant padded payload.
	payload := strings.TrimRight(parts[1], "=")
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal JWT payload: %w", err)
	}
	return claims, nil
}
