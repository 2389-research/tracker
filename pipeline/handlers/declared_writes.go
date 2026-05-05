package handlers

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

const (
	contextKeyWritesError   = "writes_error"
	contextKeyWritesWarning = "writes_warning"
	maxWritesErrorRawLen    = 512
)

func applyDeclaredWrites(node *pipeline.Node, contextUpdates map[string]string, rawJSON string, source string) (failed bool) {
	writes := dedupeDeclaredWrites(pipeline.ParseDeclaredKeys(node.Attrs["writes"]))
	if len(writes) == 0 {
		return false
	}

	// Try direct JSON parse first.
	updates, extras, err := pipeline.ExtractDeclaredWrites(writes, rawJSON)

	// If direct parse failed, try extracting JSON embedded in the text.
	if err != nil {
		if extracted, ok := pipeline.ExtractJSONFromText(rawJSON); ok {
			updates, extras, err = pipeline.ExtractDeclaredWrites(writes, extracted)
		}
	}

	// If still failed and single write key, fall back to using the raw
	// response as the value. This handles the common case where an LLM
	// writes to a file and responds with prose instead of JSON.
	if err != nil && len(writes) == 1 {
		contextUpdates[writes[0]] = rawJSON
		contextUpdates[contextKeyWritesWarning] = fmt.Sprintf(
			"node %q: writes extraction fell back to raw response for key %q (no JSON found in output)",
			node.ID, writes[0],
		)
		return false
	}

	if err != nil {
		contextUpdates[contextKeyWritesError] = formatWritesError(node.ID, writes, source, err, rawJSON)
		return true
	}

	for k, v := range updates {
		contextUpdates[k] = v
	}
	if len(extras) > 0 {
		contextUpdates[contextKeyWritesWarning] = fmt.Sprintf(
			"node %q produced extra JSON fields not declared in writes: [%s]",
			node.ID, strings.Join(extras, ", "),
		)
	}
	return false
}

func dedupeDeclaredWrites(writes []string) []string {
	if len(writes) < 2 {
		return writes
	}
	seen := make(map[string]struct{}, len(writes))
	deduped := make([]string, 0, len(writes))
	for _, write := range writes {
		if _, ok := seen[write]; ok {
			continue
		}
		seen[write] = struct{}{}
		deduped = append(deduped, write)
	}
	return deduped
}

func formatWritesError(nodeID string, writes []string, source string, parseErr error, raw string) string {
	rawPreview := raw
	if len(rawPreview) > maxWritesErrorRawLen {
		rawPreview = fmt.Sprintf("%s… (truncated, %d bytes total)", rawPreview[:maxWritesErrorRawLen], len(raw))
	}
	return fmt.Sprintf(
		"node %q declared writes: [%s]\n%s is not compatible with writes extraction: %v\nRaw output: %s",
		nodeID,
		strings.Join(writes, ", "),
		source,
		parseErr,
		rawPreview,
	)
}
