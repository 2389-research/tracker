// ABOUTME: TestMain for the tracker CLI's tests: every test starts with TRACKER_OPENAI_AUTH unset, so a
// ABOUTME: ChatGPT-mode shell or tracker run can't change what the tests build or read a real sign-in.
package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

// TestMain unsets chatgptauth.EnvVar before any test runs. A tracker run in
// ChatGPT mode passes the switch to the commands it starts (exec.CommandEnv
// keeps it), so `make ci` inside such a run would otherwise build
// ChatGPT-mode clients from the developer's real sign-in. A test that wants
// the mode sets it with t.Setenv.
func TestMain(m *testing.M) {
	if err := os.Unsetenv(chatgptauth.EnvVar); err != nil {
		fmt.Fprintf(os.Stderr, "unset %s: %v\n", chatgptauth.EnvVar, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
