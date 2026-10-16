// ABOUTME: Shared test helper building an unsigned fake JWT from arbitrary claims, so claims,
// ABOUTME: store and status tests (and later refresh tests, Tasks 4-5) can exercise the decoder.
package chatgptauth

import (
	"encoding/base64"
	"encoding/json"
)

// fakeJWT encodes claims as the payload of a three-part dot-separated token
// with a fixed header and a placeholder signature. ParseIDToken and
// AccessTokenExpiry never check either, matching Codex's own
// no-signature-check handling of these tokens (spec §1 "Identity").
func fakeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		panic("fakeJWT: marshal claims: " + err.Error())
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + ".sig"
}
