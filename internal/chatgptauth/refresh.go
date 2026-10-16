// ABOUTME: Refresh-token exchange at auth.openai.com (research §C), and RefreshError, which sorts a
// ABOUTME: failure into permanent (sign in again) or transient from the token endpoint's JSON error.
package chatgptauth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Token endpoint error codes that decide a refresh failure's class
// (codex-rs/login/src/auth/manager.rs:1659-1708).
const (
	codeRefreshTokenExpired     = "refresh_token_expired"
	codeRefreshTokenReused      = "refresh_token_reused"
	codeRefreshTokenInvalidated = "refresh_token_invalidated"
	codeInvalidGrant            = "invalid_grant"
)

// refreshCodes maps each code RefreshError.Code may carry to whether it
// alone makes the failure permanent: the three dead-refresh-token codes do
// at any status, invalid_grant only with a 400.
var refreshCodes = map[string]bool{
	codeRefreshTokenExpired:     true,
	codeRefreshTokenReused:      true,
	codeRefreshTokenInvalidated: true,
	codeInvalidGrant:            false,
}

// RefreshError reports a failed refresh-token exchange. Permanent means the
// sign-in itself is dead — the refresh token expired, was already used, or
// was revoked — and only signing in again fixes it; anything else
// (network failures included) may succeed on a retry.
type RefreshError struct {
	// Code is the server's error code when it is one this package
	// recognizes (refresh_token_expired, refresh_token_reused,
	// refresh_token_invalidated or invalid_grant), lowercased; "" for none
	// or any other. Unrecognized codes are dropped so that no text the
	// server chose ever reaches an error string.
	Code string
	// Status is the HTTP status, or 0 when no response arrived.
	Status int
	// Permanent is true for any 401, for one of the three refresh-token
	// codes, and for a 400 with invalid_grant.
	Permanent bool

	// err is the transport or decode failure behind a transient error
	// that has no error status to report.
	err error
}

// Error sends the user to `tracker setup` for a permanent failure, naming
// the code (or the status when there is none); a transient one names the
// status or the underlying failure.
func (e *RefreshError) Error() string {
	if e.Permanent {
		reason := e.Code
		if reason == "" {
			reason = fmt.Sprintf("HTTP %d", e.Status)
		}
		return "ChatGPT sign-in expired or was revoked (" + reason + "); run `tracker setup` to sign in again"
	}
	if e.err != nil {
		return "refreshing the ChatGPT sign-in failed: " + e.err.Error()
	}
	return fmt.Sprintf("refreshing the ChatGPT sign-in failed: HTTP %d", e.Status)
}

// Temporary reports whether a retry may succeed: true unless Permanent.
func (e *RefreshError) Temporary() bool { return !e.Permanent }

// Unwrap returns the transport or decode failure behind the error, if any,
// so errors.Is still finds a context cancellation.
func (e *RefreshError) Unwrap() error { return e.err }

// refreshGrant is the refresh request's JSON body. Field order is the wire
// order: grant_type, client_id, refresh_token (research §C).
type refreshGrant struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
}

// Refresh spends refreshToken at {issuer}/oauth/token for a new token set
// (research §C). Unlike the form-encoded authorization-code exchange, the
// refresh grant is a JSON body. The result holds only the fields the
// server returned: a response without a refresh_token means the server
// did not rotate it, and the caller keeps its old one. Every failure is a
// *RefreshError.
func (c *AuthClient) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	grant := refreshGrant{GrantType: "refresh_token", ClientID: ClientID, RefreshToken: refreshToken}
	var resp exchangeResponse
	var errBody map[string]any
	status, err := c.doJSONPost(ctx, c.Issuer+"/oauth/token", grant, &resp, &errBody)
	if err != nil {
		return Tokens{}, &RefreshError{Status: status, err: err}
	}
	if status != http.StatusOK {
		return Tokens{}, classifyRefreshFailure(status, tokenErrorCode(errBody))
	}
	return Tokens{IDToken: resp.IDToken, AccessToken: resp.AccessToken, RefreshToken: resp.RefreshToken}, nil
}

// tokenErrorCode picks the error code out of a token endpoint error body:
// the first non-blank string among a top-level "error" string,
// "error.code" when "error" is an object, and a top-level "code" — the
// order Codex reads them in (codex-rs/login/src/oauth/error.rs:135-147).
func tokenErrorCode(body map[string]any) string {
	nested, _ := body["error"].(map[string]any)
	for _, v := range []any{body["error"], nested["code"], body["code"]} {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// classifyRefreshFailure builds the RefreshError for a non-200 answer: the
// failure is permanent for any 401, a refresh-token code, or a 400 with
// invalid_grant; codes match case-insensitively, as in Codex.
func classifyRefreshFailure(status int, rawCode string) *RefreshError {
	code := strings.ToLower(rawCode)
	permanentCode, known := refreshCodes[code]
	if !known {
		code = ""
	}
	permanent := status == http.StatusUnauthorized ||
		permanentCode ||
		(status == http.StatusBadRequest && code == codeInvalidGrant)
	return &RefreshError{Code: code, Status: status, Permanent: permanent}
}
