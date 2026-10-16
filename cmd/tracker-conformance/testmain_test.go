// ABOUTME: TestMain for tracker-conformance's tests: every test starts with TRACKER_OPENAI_AUTH unset,
// ABOUTME: so a ChatGPT-mode shell or tracker run can't register openai from a real sign-in.
package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

// TestMain unsets chatgptauth.EnvVar before any test runs. A tracker run in
// ChatGPT mode passes the switch to the commands it starts (exec.CommandEnv
// keeps it), and in that mode createClient registers openai without an API
// key, so the no-provider tests would build a client they expect to fail.
// A test that wants the mode sets it with t.Setenv.
func TestMain(m *testing.M) {
	if err := os.Unsetenv(chatgptauth.EnvVar); err != nil {
		fmt.Fprintf(os.Stderr, "unset %s: %v\n", chatgptauth.EnvVar, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
