// ABOUTME: AuthClient, the shared HTTP client for ChatGPT device-code sign-in (and, from Task 5,
// ABOUTME: token refresh) against auth.openai.com — construction, defaults, and the request shapes.
package chatgptauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Issuer is the ChatGPT OAuth issuer: the default base URL for every
// sign-in and refresh request (research §A "Issuer / base URL"). Tests
// override AuthClient.Issuer to point at an httptest server instead.
const Issuer = "https://auth.openai.com"

// ClientID is Codex's own OAuth client ID, shared by tracker's sign-in and
// refresh requests (research §A, §B, §C).
const ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// AuthClient signs a user in to a ChatGPT subscription via the device-code
// flow. Construct one with NewAuthClient, which fills in every field's
// documented default; the zero value is not meant to be used directly.
type AuthClient struct {
	// Issuer is the base URL every request is sent against. Default: Issuer.
	Issuer string
	// HTTPClient sends every request. Default: a 30-second-timeout client.
	HTTPClient *http.Client
	// DeviceTimeout bounds how long WaitForDeviceLogin polls before
	// returning ErrDeviceCodeExpired. Default: 15 minutes (research §B).
	DeviceTimeout time.Duration
	// UserAgent is sent as every request's User-Agent header. Default:
	// UserAgent().
	UserAgent string
}

// NewAuthClient returns an AuthClient with its documented defaults.
func NewAuthClient() *AuthClient {
	return &AuthClient{
		Issuer:        Issuer,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
		DeviceTimeout: 15 * time.Minute,
		UserAgent:     UserAgent(),
	}
}

// doJSONPost POSTs body as JSON to path and, only on a 200 response,
// decodes the response into out (nil discards it). The returned status
// code is only meaningful when err is nil: a transport failure — including
// context cancellation — is reported solely through err.
func (c *AuthClient) doJSONPost(ctx context.Context, path string, body, out any) (int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(encoded))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	return c.do(req, out)
}

// doFormPost POSTs form as application/x-www-form-urlencoded to path. Same
// response/decoding contract as doJSONPost.
func (c *AuthClient) doFormPost(ctx context.Context, path string, form url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.UserAgent)
	return c.do(req, out)
}

// do sends req and, only on a 200 response, decodes the JSON body into out.
// The body is never read on any other status, so a non-200 response body
// never reaches an error string.
func (c *AuthClient) do(req *http.Request, out any) (int, error) {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// wrapRequestErr prioritizes ctx's own error over msg: a canceled or
// timed-out context is why the request failed, and callers that propagate
// it want errors.Is(err, context.Canceled) (or DeadlineExceeded) to keep
// working, not a message wrapped around it.
func wrapRequestErr(ctx context.Context, err error, msg string) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return fmt.Errorf("%s: %w", msg, err)
}
