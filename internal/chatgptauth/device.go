// ABOUTME: Device-code sign-in (design §1, research §B): request a user code, poll until the
// ABOUTME: user finishes in the browser, then exchange the server-issued PKCE pair for tokens.
package chatgptauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrDeviceCodeDisabled is returned by StartDeviceLogin when the server
// answers the usercode request with 404: a server-side feature flag, not a
// per-account setting (research §B).
var ErrDeviceCodeDisabled = errors.New("device-code sign-in is not enabled for this account (OpenAI returned 404)")

// ErrDeviceCodeExpired is returned by WaitForDeviceLogin once DeviceCode's
// deadline passes without the user finishing sign-in.
var ErrDeviceCodeExpired = errors.New("the sign-in code expired after 15 minutes; start again")

// DeviceCode is a pending device-code sign-in: the code and link to show
// the user, and the state WaitForDeviceLogin needs to poll for completion.
type DeviceCode struct {
	// VerificationURL is where the user enters UserCode: Issuer + "/codex/device".
	VerificationURL string
	// UserCode is the one-time code to show the user.
	UserCode string
	// Interval is how long to wait between polls. A caller that needs a
	// faster poll (tests) may lower it after StartDeviceLogin returns.
	Interval time.Duration
	// ExpiresAt is when the code stops being valid: start + DeviceTimeout.
	ExpiresAt time.Time

	// deviceAuthID identifies this login to the poll endpoint. It isn't a
	// credential, but there's no reason to expose it outside this package
	// either.
	deviceAuthID string
}

// usercodeResponse is POST {issuer}/api/accounts/deviceauth/usercode's body
// (research §B, device_code_auth.rs:27-34). The user code field is spelled
// "user_code" or, on some deployments, "usercode"; userCode() resolves
// whichever one the server sent.
type usercodeResponse struct {
	DeviceAuthID        string       `json:"device_auth_id"`
	UserCodeField       string       `json:"user_code"`
	UserCodeAltSpelling string       `json:"usercode"`
	Interval            flexInterval `json:"interval"`
}

// userCode resolves the response's user code under either field spelling.
func (r usercodeResponse) userCode() string {
	if r.UserCodeField != "" {
		return r.UserCodeField
	}
	return r.UserCodeAltSpelling
}

// interval resolves the response's interval to a duration, defaulting to 5
// seconds when the server sent none, or a non-positive one.
func (r usercodeResponse) interval() time.Duration {
	if r.Interval <= 0 {
		return 5 * time.Second
	}
	return time.Duration(r.Interval) * time.Second
}

// flexInterval decodes a JSON "interval" field that the server sends as
// either a bare number or a quoted string holding one (research §B,
// device_code_auth.rs:27-34) into a whole number of seconds.
type flexInterval int64

func (f *flexInterval) UnmarshalJSON(data []byte) error {
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*f = flexInterval(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.New("interval: neither a number nor a string")
	}
	parsed, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("interval: %w", err)
	}
	*f = flexInterval(parsed)
	return nil
}

// pollResponse is POST {issuer}/api/accounts/deviceauth/token's 200 body
// (research §B, CodeSuccessResp, device_code_auth.rs:55-60). The server's
// own code_challenge is part of the wire shape but unused here: the
// exchange only needs the verifier.
type pollResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

// exchangeResponse is POST {issuer}/oauth/token's 200 body. The
// authorization_code grant must carry all three tokens (research §A,
// ExchangedTokens); the refresh grant may omit any of them (research §C,
// RefreshResponse).
type exchangeResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// missingAField reports whether any of the three required tokens is empty.
func (r exchangeResponse) missingAField() bool {
	return r.IDToken == "" || r.AccessToken == "" || r.RefreshToken == ""
}

// StartDeviceLogin requests a user code from the server (research §B step
// 1). ErrDeviceCodeDisabled reports a 404 — device-code sign-in not enabled
// for this server.
func (c *AuthClient) StartDeviceLogin(ctx context.Context) (DeviceCode, error) {
	start := time.Now()
	var resp usercodeResponse
	status, err := c.doJSONPost(ctx, c.Issuer+"/api/accounts/deviceauth/usercode",
		map[string]string{"client_id": ClientID}, &resp, nil)
	if err != nil {
		return DeviceCode{}, wrapRequestErr(ctx, err, "request device code")
	}
	if status == http.StatusNotFound {
		return DeviceCode{}, ErrDeviceCodeDisabled
	}
	if status != http.StatusOK {
		return DeviceCode{}, fmt.Errorf("request device code: HTTP %d", status)
	}

	return DeviceCode{
		VerificationURL: c.Issuer + "/codex/device",
		UserCode:        resp.userCode(),
		Interval:        resp.interval(),
		ExpiresAt:       start.Add(c.DeviceTimeout),
		deviceAuthID:    resp.DeviceAuthID,
	}, nil
}

// WaitForDeviceLogin polls until the user finishes sign-in in the browser,
// then exchanges the server-issued code for tokens (research §B steps
// 2-3). It returns ErrDeviceCodeExpired once dc.ExpiresAt passes, or
// ctx.Err() promptly on cancellation.
func (c *AuthClient) WaitForDeviceLogin(ctx context.Context, dc DeviceCode) (Tokens, error) {
	for {
		tokens, done, err := c.pollRound(ctx, dc)
		if done {
			return tokens, err
		}
	}
}

// pollRound runs one round of WaitForDeviceLogin's loop: an
// expiry/cancellation check, one poll, and either the final token exchange
// or a capped wait for the next poll. done reports whether the loop should
// stop now — on success, on any error, or once expiry/cancellation ends
// the wait — and is false only when the caller should poll again.
func (c *AuthClient) pollRound(ctx context.Context, dc DeviceCode) (Tokens, bool, error) {
	if err := ctx.Err(); err != nil {
		return Tokens{}, true, err
	}
	if !time.Now().Before(dc.ExpiresAt) {
		return Tokens{}, true, ErrDeviceCodeExpired
	}

	code, verifier, pending, err := c.pollDeviceCode(ctx, dc)
	if err != nil {
		return Tokens{}, true, err
	}
	if !pending {
		tokens, err := c.exchangeDeviceCode(ctx, code, verifier)
		return tokens, true, err
	}

	if err := waitForNextPoll(ctx, dc.Interval, dc.ExpiresAt); err != nil {
		return Tokens{}, true, err
	}
	return Tokens{}, false, nil
}

// pollDeviceCode sends one poll request. pending is true on a 403 or 404
// (research §B step 2, "still pending"); any other non-200 status is a
// terminal error that never repeats the response body.
func (c *AuthClient) pollDeviceCode(ctx context.Context, dc DeviceCode) (code, verifier string, pending bool, err error) {
	var resp pollResponse
	status, err := c.doJSONPost(ctx, c.Issuer+"/api/accounts/deviceauth/token",
		map[string]string{"device_auth_id": dc.deviceAuthID, "user_code": dc.UserCode}, &resp, nil)
	if err != nil {
		return "", "", false, wrapRequestErr(ctx, err, "poll device sign-in")
	}
	switch status {
	case http.StatusOK:
		return resp.AuthorizationCode, resp.CodeVerifier, false, nil
	case http.StatusForbidden, http.StatusNotFound:
		return "", "", true, nil
	default:
		return "", "", false, fmt.Errorf("device sign-in failed: HTTP %d", status)
	}
}

// waitForNextPoll sleeps for interval, capped so it never waits past
// deadline, honoring ctx cancellation via a select on ctx.Done() and a
// timer. A deadline already reached (or inside the cap) ends the wait with
// ErrDeviceCodeExpired instead of sleeping for a zero or negative duration.
func waitForNextPoll(ctx context.Context, interval time.Duration, deadline time.Time) error {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if remaining := time.Until(deadline); remaining < interval {
		interval = remaining
	}
	if interval <= 0 {
		return ErrDeviceCodeExpired
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// exchangeDeviceCode trades the server-issued authorization code and PKCE
// verifier for tokens (research §B step 3, reusing the browser flow's
// exchange with redirect_uri = {issuer}/deviceauth/callback). The response
// must carry all three tokens; AccountID comes from the id_token.
func (c *AuthClient) exchangeDeviceCode(ctx context.Context, code, verifier string) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"redirect_uri":  {c.Issuer + "/deviceauth/callback"},
		"code_verifier": {verifier},
	}
	var resp exchangeResponse
	status, err := c.doFormPost(ctx, c.Issuer+"/oauth/token", form, &resp)
	if err != nil {
		return Tokens{}, wrapRequestErr(ctx, err, "exchange device sign-in code")
	}
	if status != http.StatusOK {
		return Tokens{}, fmt.Errorf("exchange device sign-in code: HTTP %d", status)
	}
	if resp.missingAField() {
		return Tokens{}, errors.New("exchange device sign-in code: response is missing a required token field")
	}

	identity, err := ParseIDToken(resp.IDToken)
	if err != nil {
		return Tokens{}, fmt.Errorf("parse chatgpt identity: %w", err)
	}
	return Tokens{
		IDToken:      resp.IDToken,
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		AccountID:    identity.AccountID,
		LastRefresh:  time.Now(),
	}, nil
}
