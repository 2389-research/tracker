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
	// maxFallbackValueBytes caps the size of the raw-response fallback value
	// that lands in a context key. Without a cap, a 10MB tool stdout (the
	// hard ceiling) or unbounded LLM response would be persisted into
	// status.json, activity.jsonl, checkpoints, and inlined into downstream
	// prompts.
	maxFallbackValueBytes = 8192
)

func applyDeclaredWrites(node *pipeline.Node, contextUpdates map[string]string, rawJSON string, source string) (failed bool) {
	writes := dedupeDeclaredWrites(pipeline.ParseDeclaredKeys(node.Attrs["writes"]))
	if len(writes) == 0 {
		return false
	}

	// Reject writes-key collisions with two reserved sets:
	//
	//   1. The tool_command safe-key allowlist (outcome, preferred_label,
	//      human_response, interview_answers) — enforced fail-closed in
	//      tool_command expansion to keep LLM output out of shell input. A
	//      workflow declaring `writes: outcome` would funnel LLM-controlled
	//      content into the reserved name and bypass the gate.
	//
	//   2. The declared-writes signal keys (writes_error, writes_warning) —
	//      these are runtime observability keys set by this function for
	//      `tracker diagnose` and downstream nodes to branch on. Allowing a
	//      workflow to set them via writes would let an LLM spoof a failure
	//      / healed-warning signal that wasn't real, undermining diagnose UX.
	//
	// Either collision is treated as an authoring error: refuse the node
	// rather than silently land the data.
	for _, key := range writes {
		if pipeline.IsToolCommandSafeCtxKey(key) || isReservedWritesSignalKey(key) {
			contextUpdates[contextKeyWritesError] = fmt.Sprintf(
				"node %q: declared writes key %q collides with a reserved name; tool_command safe-key allowlist (outcome/preferred_label/human_response/interview_answers) and writes signal keys (writes_error/writes_warning) cannot be set from declared writes",
				node.ID, key,
			)
			return true
		}
	}

	// Healing cascade: direct JSON → embedded JSON → raw fallback.
	updates, extras, err := pipeline.ExtractDeclaredWrites(writes, rawJSON)

	// If direct parse failed, try extracting JSON embedded in the text.
	// Track whether extraction surfaced any JSON object — even if that JSON
	// turned out to be missing the declared key, we don't want to silently
	// fall back to raw prose (that would mask a real contract failure).
	foundExtractableJSON := false
	if err != nil {
		if extracted, ok := pipeline.ExtractJSONFromText(rawJSON); ok {
			foundExtractableJSON = true
			updates, extras, err = pipeline.ExtractDeclaredWrites(writes, extracted)
		}
	}

	// Single-key fallback: only when no JSON was extractable AT ALL. A model
	// that returned valid JSON missing the declared key gets a hard contract
	// failure (below) rather than silently shipping the entire raw response
	// under a name it doesn't fit. Cap the fallback value size so tool
	// stdout or long LLM responses don't bloat downstream artifacts.
	if err != nil && len(writes) == 1 && !foundExtractableJSON {
		contextUpdates[writes[0]] = capFallbackValue(rawJSON)
		contextUpdates[contextKeyWritesWarning] = fmt.Sprintf(
			"node %q: writes extraction fell back to raw response for key %q (no JSON found in output)",
			node.ID, writes[0],
		)
		return false
	}

	if err != nil {
		// If we DID find extractable JSON but it failed the contract, the
		// generic "raw output not parseable as JSON" message would mislead.
		// Distinguish the two failure modes so `tracker diagnose` can guide
		// the user to the actual problem.
		if foundExtractableJSON {
			contextUpdates[contextKeyWritesError] = fmt.Sprintf(
				"node %q declared writes: [%s]\n%s contained extractable JSON but failed the writes contract: %v",
				node.ID, strings.Join(writes, ", "), source, err,
			)
		} else {
			contextUpdates[contextKeyWritesError] = formatWritesError(node.ID, writes, source, err, rawJSON)
		}
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

// isReservedWritesSignalKey reports whether key is one of the runtime
// observability signal names used by applyDeclaredWrites itself. Workflow
// authors must not declare these as writes targets — the runtime owns
// them, and letting an LLM set them would spoof the signal.
func isReservedWritesSignalKey(key string) bool {
	return key == contextKeyWritesError || key == contextKeyWritesWarning
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

// capFallbackValue caps a raw-response fallback value at maxFallbackValueBytes.
// Without a cap, a multi-megabyte tool stdout or long LLM response would land
// in a context key and from there into status.json, activity.jsonl,
// checkpoints, and downstream prompts. Truncation marker tells the user the
// stored value isn't the whole response.
func capFallbackValue(s string) string {
	if len(s) <= maxFallbackValueBytes {
		return s
	}
	return fmt.Sprintf(
		"%s\n\n…(truncated at %d bytes; original was %d bytes)",
		s[:maxFallbackValueBytes], maxFallbackValueBytes, len(s),
	)
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
