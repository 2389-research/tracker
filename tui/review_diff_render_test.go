// ABOUTME: A diff shown at a review gate must keep its +/- lines through the glamour render:
// ABOUTME: quickfix fences it; unfenced, added and removed lines collapse into bullets and prose.
package tui

import (
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestReviewRender_FencedDiffKeepsAddedAndRemovedLines(t *testing.T) {
	md := "## Diff against abc\n\n`````diff\n--- a/main.go\n+++ b/main.go\n@@ -1,2 +1,2 @@\n-// Hello says hi\n+// Hello says hello\n-\treturn \"hi\"\n+\treturn \"hello\"\n`````\n"
	out := ansi.ReplaceAllString(renderMarkdownForReview(md, 100), "")
	for _, want := range []string{"-// Hello says hi", "+// Hello says hello", "-\treturn \"hi\"", "+\treturn \"hello\""} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(strings.ReplaceAll(line, "    ", "\t"), want) || strings.Contains(line, strings.ReplaceAll(want, "\t", "    ")) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("rendered gate lost %q as its own line:\n%s", want, out)
		}
	}
}
