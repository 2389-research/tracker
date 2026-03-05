// ABOUTME: Output truncation for agent tool results.
// ABOUTME: Keeps the tail of long outputs to preserve error messages, with a marker showing truncated amount.
package tools

import "fmt"

// maxToolOutputLen is the maximum character length for tool output (~2000 tokens).
const maxToolOutputLen = 8000

// truncateOutput truncates output that exceeds maxLen, keeping the tail portion
// to preserve error messages and recent output. A marker is prepended showing
// how many characters were removed.
func truncateOutput(output string, maxLen int) string {
	if len(output) <= maxLen {
		return output
	}

	tailLen := maxLen / 2
	tail := output[len(output)-tailLen:]
	truncatedCount := len(output) - tailLen
	marker := fmt.Sprintf("[... truncated %d characters ...]\n", truncatedCount)

	return marker + tail
}
