// ABOUTME: Tests that trackerbot refuses TRACKER_OPENAI_AUTH=chatgpt: a ChatGPT subscription login is
// ABOUTME: for one person's own use, so a shared Slack bot must not run on it (design D3).
package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

const wantRefusal = "trackerbot won't run with TRACKER_OPENAI_AUTH=chatgpt: a ChatGPT subscription login is for one person's own use, not a shared Slack bot; use an OpenAI API key instead"

func TestCheckOpenAIAuth(t *testing.T) {
	cases := []struct {
		value   string
		wantErr string // "" means trackerbot may start
	}{
		{"", ""},
		{"api-key", ""},
		{"chatgpt", wantRefusal},
		{" chatgpt ", wantRefusal},
		{"yes", `trackerbot: invalid TRACKER_OPENAI_AUTH="yes": must be "api-key" or "chatgpt"`},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(chatgptauth.EnvVar, tc.value)
			err := checkOpenAIAuth()
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.wantErr {
				t.Errorf("checkOpenAIAuth() with %s=%q = %q, want %q", chatgptauth.EnvVar, tc.value, got, tc.wantErr)
			}
		})
	}
}

// TestTrackerbotRefusesToStartInChatGPTMode runs main in a child process
// (this test binary, re-run with mainChildEnv set) with the switch on and no
// Slack tokens. It must exit 1 with the refusal on stderr, before it looks
// at the Slack tokens at all.
func TestTrackerbotRefusesToStartInChatGPTMode(t *testing.T) {
	const mainChildEnv = "TRACKERBOT_TEST_RUN_MAIN"
	if os.Getenv(mainChildEnv) == "1" {
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTrackerbotRefusesToStartInChatGPTMode$")
	cmd.Env = append(os.Environ(), mainChildEnv+"=1", chatgptauth.EnvVar+"=chatgpt", "SLACK_BOT_TOKEN=", "SLACK_APP_TOKEN=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("trackerbot with %s=chatgpt: err = %v, want exit status 1; stderr:\n%s", chatgptauth.EnvVar, err, stderr.String())
	}
	if !strings.Contains(stderr.String(), wantRefusal) {
		t.Errorf("stderr lacks the refusal %q:\n%s", wantRefusal, stderr.String())
	}
}
