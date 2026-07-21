// ABOUTME: Tests Slack-specific hardening: the button action-id codec and authz.
package main

import "testing"

// TestGateActionRoundTrip pins the button action-id codec: every gate id
// round-trips (including one containing the "|" separator), and foreign or
// malformed action ids are rejected so a click never misroutes.
func TestGateActionRoundTrip(t *testing.T) {
	for i, id := range []string{"g1", "a|b", "", "xY_-9", "deadbeefcafef00d"} {
		got, ok := parseGateAction(gateActionID(id, i))
		if !ok || got != id {
			t.Errorf("round-trip %q (i=%d): got (%q, %v)", id, i, got, ok)
		}
	}
	if _, ok := parseGateAction("other|0|x"); ok {
		t.Error("a foreign action id must not parse")
	}
	if _, ok := parseGateAction("nopipe"); ok {
		t.Error("a malformed action id must not parse")
	}
}

// TestSlackBot_Authorized covers the allowlist gate: empty = open, otherwise
// only listed users (trimmed) pass.
func TestSlackBot_Authorized(t *testing.T) {
	b := &SlackBot{}
	if !b.authorized("U1") {
		t.Fatal("an empty allowlist must be open")
	}
	b.SetAllowlist([]string{"U1", " U2 ", ""})
	if !b.authorized("U1") || !b.authorized("U2") {
		t.Fatal("allowlisted users must be authorized")
	}
	if b.authorized("U3") {
		t.Fatal("a non-allowlisted user must be rejected")
	}
	if b.authorized("") {
		t.Fatal("an empty user id must be rejected under a non-empty allowlist")
	}
}
