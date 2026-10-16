// ABOUTME: Tests that UserAgent() produces the "tracker/<version> (<GOOS>; <GOARCH>)" shape every
// ABOUTME: chatgptauth request sends as its User-Agent header.
package chatgptauth

import (
	"regexp"
	"testing"
)

func TestUserAgentShape(t *testing.T) {
	ua := UserAgent()
	re := regexp.MustCompile(`^tracker/\S+ \(\w+; \w+\)$`)
	if !re.MatchString(ua) {
		t.Errorf("UserAgent() = %q, want a match of %s", ua, re.String())
	}
}
