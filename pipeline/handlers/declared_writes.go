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

	updates, extras, err := pipeline.ExtractDeclaredWrites(writes, rawJSON)
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
