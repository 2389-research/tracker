package handlers

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

const (
	contextKeyWritesError   = "writes_error"
	contextKeyWritesWarning = "writes_warning"
)

func applyDeclaredWrites(node *pipeline.Node, contextUpdates map[string]string, rawJSON string, source string) (failed bool) {
	writes := pipeline.ParseDeclaredKeys(node.Attrs["writes"])
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

func formatWritesError(nodeID string, writes []string, source string, parseErr error, raw string) string {
	return fmt.Sprintf(
		"node %q declared writes: [%s]\n%s is not compatible with writes extraction: %v\nRaw output: %s",
		nodeID,
		strings.Join(writes, ", "),
		source,
		parseErr,
		raw,
	)
}
