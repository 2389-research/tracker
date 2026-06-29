// ABOUTME: Opt-in content-hash node-output memoization (#421).
// ABOUTME: Replays a prior successful outcome when a loop-restart re-enters a node with identical inputs.
package pipeline

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
)

// memoizeEnabled reports whether a node opted in to output memoization via the
// generic `memoize: true` param (surfaced through node.Attrs without a
// dippin-lang field — see the agent Params pass-through in dippin_adapter.go).
// Truthy is exactly "true" (case-insensitive, trimmed); any other value
// (absent, "false", "1") is off. Default-off is an acceptance criterion: a
// .dip file lacking the attr never reaches the memo lookup or store sites.
func memoizeEnabled(execNode *Node) bool {
	return strings.EqualFold(strings.TrimSpace(execNode.Attrs["memoize"]), "true")
}

// writeLP writes a length-prefixed string into the hash buffer: a uint32
// big-endian length followed by the raw bytes. The length prefix prevents
// concatenation collisions (e.g. "a"+"bc" hashing the same as "ab"+"c").
func writeLP(h interface{ Write([]byte) (int, error) }, s string) {
	var lenbuf [4]byte
	binary.BigEndian.PutUint32(lenbuf[:], uint32(len(s)))
	_, _ = h.Write(lenbuf[:])
	_, _ = h.Write([]byte(s))
}

// computeMemoKey returns a SHA-256 hex digest over the node's RESOLVED inputs,
// and ok=false on any condition that makes a replay unsafe (memoize disabled,
// or a hashing input could not be obtained). When ok=false the caller treats
// the node as a cache MISS and runs the handler — we never replay on an
// uncertain key (CLAUDE.md: never silently swallow / never replay stale).
//
// The digest is computed over a length-prefixed buffer in this FIXED order.
// Document changes here and bump the "v1" recipe tag to auto-invalidate.
//
//  1. "v1"                 — recipe-version tag.
//  2. execNode.ID          — scopes the key to this node.
//  3. execNode.Handler     — handler identity.
//  4. execNode.Attrs        — every entry, sorted by key, each writeLP(k)+writeLP(v).
//     These are POST-stylesheet, POST-ExpandGraphVariables, POST-ExpandPromptVariables
//     (prepareExecNode), so the interpolated `prompt`/`command` already embeds
//     every upstream ctx.* value it referenced via ${...}.
//  5. bare-namespace ctx snapshot — every context key WITHOUT a "node." prefix,
//     EXCLUDING the three routing-scratch keys reset immediately before execution
//     (outcome, preferred_label, suggested_next_nodes — they are reset to "" so
//     excluding them is a documented no-op). Sorted, each writeLP(k)+writeLP(v).
//     Rationale: ${...} interpolation only captures ctx values literally embedded
//     in an attr; a handler can READ context (last_response, tool_stdout, ...) it
//     was never templated with. Over-hashing the bare namespace makes ANY upstream
//     drift invalidate — conservative by design (over-invalidate, never replay stale).
//  6. tree fingerprint     — REQUIRED when the node declares writable_paths (an
//     agent with working-tree side effects). writeLP("tree")+writeLP(sha). If no
//     git repo is active (s.gitRepo == nil) or the tree hash ERRORS, return
//     ok=false (hard miss) — never replay a side-effecting node whose tree input
//     cannot be proven unchanged.
//
// The opt-in contract: by setting memoize:true the operator asserts the node is
// a pure function of these hashed inputs. Non-deterministic output (timestamps,
// LLM variance) is the operator's responsibility, not the engine's.
func (e *Engine) computeMemoKey(s *runState, execNode *Node) (string, bool) {
	if !memoizeEnabled(execNode) {
		return "", false
	}

	h := sha256.New()
	writeLP(h, "v1")
	writeLP(h, execNode.ID)
	writeLP(h, execNode.Handler)
	hashSortedMap(h, execNode.Attrs)                      // 4. resolved attrs
	hashSortedMap(h, memoizableContext(s.pctx, execNode)) // 5. bare ctx inputs

	// 6. tree fingerprint for side-effecting agents (writable_paths). A node that
	// declares writable_paths has working-tree side effects, so a usable tree
	// fingerprint is REQUIRED to prove its tree input is unchanged before replay.
	// If no git repo is active (s.gitRepo == nil — the default tracker run /
	// library Run config, since WithGitArtifacts has no production callers) or
	// the fingerprint errors, return ok=false (hard miss → handler re-runs).
	// Never replay a side-effecting node when its tree input cannot be proven
	// unchanged (CLAUDE.md: over-invalidate, never replay stale).
	if strings.TrimSpace(execNode.Attrs["writable_paths"]) != "" {
		if s.gitRepo == nil {
			return "", false
		}
		sha, err := s.gitRepo.TreeFingerprint()
		if err != nil {
			// Uncertain key — hard miss, never replay. Caller emits a warning.
			return "", false
		}
		writeLP(h, "tree")
		writeLP(h, sha)
	}

	return hex.EncodeToString(h.Sum(nil)), true
}

// hashSortedMap writes a map into the hash buffer in key-sorted order, each
// entry as writeLP(k)+writeLP(v), so map iteration order never affects the key.
func hashSortedMap(h interface{ Write([]byte) (int, error) }, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeLP(h, k)
		writeLP(h, m[k])
	}
}

// memoizableContext returns the subset of the bare-namespace context that is a
// genuine INPUT to execNode (step 5 of computeMemoKey). It excludes:
//   - per-node scoped keys (node.* prefix),
//   - routing-scratch keys reset before execution (outcome, preferred_label,
//     suggested_next_nodes),
//   - bare keys this node itself wrote on a prior entry AND that still hold that
//     written value. After a node runs its dirty bare keys are aliased to
//     node.<id>.<key> (ScopeToNode); on loop re-entry those bare values linger,
//     so hashing them would fold the node's OWN output into its input key and
//     defeat replay. A bare key K is treated as a self-output (skipped) ONLY
//     when node.<execNode.ID>.K exists AND the current bare K equals it. If an
//     intervening node overwrote bare K with a DIFFERENT value before a loop
//     restart, K is now a genuine changed INPUT and is hashed — otherwise the
//     node could replay stale output against changed input (#425 review).
//     Upstream keys never carry this node's prefix, so they survive — blind only
//     to unchanged self-outputs.
//
// EXCEPTION — ContextKeyLastResponse and ContextKeyHumanResponse are ALWAYS
// hashed, even when this node wrote them on a prior entry. InjectPipelineContext
// appends these two bare keys to EVERY agent prompt at runtime (FidelityFull,
// the default), so they are genuine prompt INPUTS, not merely self-outputs. In a
// build->review--fail-->build loop an intervening node overwrites bare
// last_response with a new critique that becomes part of build's resolved prompt
// on re-entry; the self-output exclusion would otherwise drop it and replay a
// stale outcome. Hashing them unconditionally is the conservative direction: a
// node that loops directly to itself with its own lingering last_response now
// re-runs (miss) rather than risk a stale replay (over-invalidate, never replay
// stale).
func memoizableContext(pctx *PipelineContext, execNode *Node) map[string]string {
	snap := pctx.Snapshot()
	selfScopePrefix := "node." + execNode.ID + "."
	out := make(map[string]string, len(snap))
	for k, v := range snap {
		if isMemoizableContextKey(k, v, selfScopePrefix, snap) {
			out[k] = v
		}
	}
	return out
}

// memoOutcome rebuilds a replay Outcome from a stored MemoEntry, deep-copying
// the reference-typed fields (ContextUpdates map, SuggestedNextNodes slice) so
// the replay path and anything it hands the Outcome to (e.g. s.lastOutcome via
// applyOutcome) can never mutate the checkpoint memo record (#425 review).
func memoOutcome(rec MemoEntry) *Outcome {
	var ctxUpdates map[string]string
	if rec.ContextUpdates != nil {
		ctxUpdates = make(map[string]string, len(rec.ContextUpdates))
		for k, v := range rec.ContextUpdates {
			ctxUpdates[k] = v
		}
	}
	var suggested []string
	if rec.SuggestedNextNodes != nil {
		suggested = append([]string(nil), rec.SuggestedNextNodes...)
	}
	return &Outcome{
		Status:             rec.Status,
		ContextUpdates:     ctxUpdates,
		PreferredLabel:     rec.PreferredLabel,
		SuggestedNextNodes: suggested,
	}
}

// isMemoizableContextKey reports whether bare context key k is a genuine input
// to hash (see memoizableContext). It rejects node-scoped keys, routing-scratch
// keys, and self-outputs — but ContextKeyLastResponse / ContextKeyHumanResponse
// are always accepted (the EXCEPTION: they are injected prompt inputs).
func isMemoizableContextKey(k, v, selfScopePrefix string, snap map[string]string) bool {
	if strings.HasPrefix(k, "node.") {
		return false
	}
	if k == ContextKeyOutcome || k == ContextKeyPreferredLabel || k == ContextKeySuggestedNextNodes {
		return false
	}
	if k == ContextKeyLastResponse || k == ContextKeyHumanResponse {
		return true
	}
	scoped, selfAliased := snap[selfScopePrefix+k]
	if !selfAliased {
		return true // never this node's output — a genuine upstream input
	}
	// Self-aliased on a prior pass. Skip it as stale self-output ONLY while the
	// bare value is unchanged; if an intervening node overwrote it, it is now a
	// genuine changed input and must be hashed (#425 review).
	return v != scoped
}
