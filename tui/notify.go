// ABOUTME: Desktop notification utility — fires OS-native notifications on pipeline completion.
// ABOUTME: Uses osascript on macOS, notify-send on Linux. Respects TRACKER_NO_NOTIFY env var.
package tui

import (
	"os"
	"os/exec"
	"runtime"
)

// SendNotification sends a desktop notification. Fire-and-forget: errors are
// silently ignored. Respects TRACKER_NO_NOTIFY=1 env var to disable.
func SendNotification(title, body string) {
	if os.Getenv("TRACKER_NO_NOTIFY") != "" {
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := `display notification "` + escapeOsascript(body) + `" with title "` + escapeOsascript(title) + `"`
		cmd = exec.Command("osascript", "-e", script)
	case "linux":
		cmd = exec.Command("notify-send", title, body)
	default:
		return
	}
	// Run in a goroutine to avoid blocking and prevent zombie processes.
	go func() { _ = cmd.Run() }()
}

// escapeOsascript escapes double quotes and backslashes for osascript strings.
func escapeOsascript(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
