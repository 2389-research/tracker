// ABOUTME: configureJail wires the writable_paths fs-jail into the agent's exec environment.
// ABOUTME: Three refuse-to-start gates (bad paths, unsupported backend, Landlock unavailable);
// ABOUTME: writable_paths_mode: prefer degrades ONLY the Landlock gate to an unjailed run (#648).
package handlers

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

// refuseWritablePathsOnUnsupportedBackend is the dispatcher-layer
// counterpart to configureJail's G2 gate. configureJail only runs inside
// NativeBackend.Run; the claude-code and acp backends never invoke it
// because buildRunConfig switches Extra away from *agent.SessionConfig
// for them, so the writable_paths signal is dropped before any gate fires.
// Without an earlier check a node that declares writable_paths but selects
// a non-native backend (either via `backend: claude-code` / `backend: acp`
// on the node OR via the global --backend default with no node-level
// override) starts unjailed (#275 review, Copilot codergen.go:647).
//
// The detection is by type assertion against the backend instance: anything
// that isn't *NativeBackend cannot run inside the jail, regardless of how
// it got selected.
func refuseWritablePathsOnUnsupportedBackend(node *pipeline.Node, backend pipeline.AgentBackend) error {
	if node == nil {
		return nil
	}
	cfg := node.AgentConfig(nil)
	if !cfg.WritablePathsSet {
		return nil
	}
	if _, ok := backend.(*NativeBackend); ok {
		return nil
	}
	return fmt.Errorf("writable_paths refuses backend %T (only native enforces; out-of-process backends cannot be sandboxed; see issue #272)", backend)
}

// jailRefusedError marks a writable_paths refuse-to-start (any of the G1/G2/G3
// gates, or a non-local exec environment). It is a host-capability /
// configuration condition — Landlock missing, malformed globs, out-of-process
// backend — that retrying can never change, so handleRunError classifies it as
// a non-retryable, routable OutcomeFail instead of the OutcomeRetry default
// (#642). Pre-#642 the retry default plus a fallback_retry_target that led back
// to the node produced an endless refuse -> retry -> fallback cycle.
type jailRefusedError struct{ err error }

func (e *jailRefusedError) Error() string { return e.err.Error() }
func (e *jailRefusedError) Unwrap() error { return e.err }

// jailSetup is what setupJail decided for one session (#648).
//
//	Enabled  — the full jail is wired: CommandWrapper (Landlock via __jail-exec)
//	           + WriteOpener/Remover (openat2). Identical to the pre-#648 (true, nil).
//	Degraded — non-nil ONLY when cfg.WritablePathsMode == "prefer" AND the G3
//	           host-capability probe failed: the node runs with an UNJAILED
//	           Bash subprocess; WriteOpener/Remover are still installed with
//	           the glob policy (best-effort tier, spec C6). Enabled is false.
//
// Both false/nil means WritablePathsSet was false — no jail, env unchanged.
type jailSetup struct {
	Enabled  bool
	Degraded *pipeline.JailDegradedDetail
}

// configureJail is the pre-#648 entry point, kept with its exact signature and
// semantics so every existing jail test and caller is unchanged (spec C2). It
// is setupJail with the degrade signal dropped: under require the two are
// identical; under prefer a degraded setup reads as (false, nil) here —
// callers that need to observe the degrade use setupJail.
func configureJail(cfg *agent.SessionConfig, env *execpkg.LocalEnvironment, processCwd string) (bool, error) {
	setup, err := setupJail(cfg, env, processCwd)
	return setup.Enabled, err
}

// setupJail consults cfg.WritablePathsSet and wires the jail into env when
// the flag is set. Returns (setup, err):
//   - ({false, nil}, nil) when WritablePathsSet is false — no jail, env unchanged.
//   - ({false, nil}, err) when a refuse-to-start gate fires — session creation halts.
//   - ({true, nil}, nil) when the jail is fully wired — env.CommandWrapper,
//     env.WriteOpener and env.Remover are populated.
//   - ({false, &detail}, nil) when mode is "prefer" and ONLY the G3 host
//     probe failed (#648) — env.WriteOpener/Remover carry the glob policy,
//     env.CommandWrapper is NOT installed (Bash is UNJAILED).
//
// Refusal gates (per spec § 8.4), checked in this order (spec C10):
//
//	G1. ValidateWritablePaths returns an error (covers bad working_dir, bad
//	    globs, empty list — Task 8 unifies all three classes). AUTHORING:
//	    refuses in both modes.
//	G2. Backend is claude-code or acp (out-of-process; jail can't enforce)
//	    OR unknown (fail-closed). BACKEND: refuses in both modes.
//	G3. ProbeLandlock fails (non-Linux, Landlock ABI < 3 i.e. kernel < 6.2,
//	    syscall denied). HOST-CAPABILITY: refuses under require; degrades
//	    under prefer (spec C4).
//
// The handoff: NativeBackend.Run calls this immediately before
// agent.NewSession with a fresh *LocalEnvironment rooted at the resolved
// session working_dir. Any refuse returned here surfaces as the
// SessionResult error wrapped in jailRefusedError, which
// CodergenHandler.handleRunError turns into a non-retryable OutcomeFail
// pre-LLM-token (#642); the session never starts. claude-code
// and acp backends never reach this function — they're refused earlier
// at refuseWritablePathsOnUnsupportedBackend in CodergenHandler.Execute
// (round 7) because buildRunConfig drops the SessionConfig signal for
// them before any backend.Run is dispatched.
func setupJail(cfg *agent.SessionConfig, env *execpkg.LocalEnvironment, processCwd string) (jailSetup, error) {
	if !cfg.WritablePathsSet {
		return jailSetup{}, nil
	}

	// G1: validate the working_dir + glob shape. Catches empty list, malformed
	// glob, working_dir escape — all three classes per Task 8.
	if err := execpkg.ValidateWritablePaths(cfg.WorkingDir, cfg.WritablePaths, processCwd); err != nil {
		return jailSetup{}, fmt.Errorf("writable_paths validation failed: %w", err)
	}

	// G2: refuse unsupported backends (BACKEND class — both modes).
	if err := refuseUnsupportedJailBackend(cfg.Backend); err != nil {
		return jailSetup{}, err
	}

	// Wire the env. The anchor is the absolute resolved WorkingDir.
	anchor := jailAnchor(cfg.WorkingDir, processCwd)
	// Normalize the stored globs to the same canonical (path.Clean) form the
	// validator checked. ValidateWritablePaths Cleans each entry before its
	// escape/shape checks, but the runtime matcher (matchOneGlob) and the
	// Landlock dir computation (landlockDirForGlob) both consume the stored
	// string literally. Without normalizing here, an entry like "./workspace/**"
	// passes validation yet makes matchOneGlob compare the literal prefix
	// "./workspace" against "workspace/..." and deny every write under
	// workspace/ — a fail-closed surprise (Copilot codergen_jail.go:71).
	globs := make([]string, len(cfg.WritablePaths))
	for i, g := range cfg.WritablePaths {
		globs[i] = path.Clean(g)
	}

	// G3: probe Landlock support (HOST-CAPABILITY class).
	if err := execpkg.ProbeLandlock(); err != nil {
		return landlockUnavailable(cfg, env, anchor, globs, err)
	}

	installEnforcedJail(env, anchor, globs)
	return jailSetup{Enabled: true}, nil
}

// refuseUnsupportedJailBackend is gate G2. claude-code and acp run
// out-of-process, so the jail can't intercept their writes. Unknown names
// also refuse — safer to fail-closed than to ship a silent no-op on a future
// backend. Mode-agnostic: a backend refusal never degrades (#648 spec C4).
func refuseUnsupportedJailBackend(backend string) error {
	switch backend {
	case "", "native":
		return nil
	case "claude-code", "acp":
		return fmt.Errorf("writable_paths is not supported on backend %q (only native enforces; see issue #272)", backend)
	default:
		return fmt.Errorf("writable_paths refuses unknown backend %q (only native enforces; see issue #272)", backend)
	}
}

// jailAnchor resolves the absolute jail root from the session working_dir.
func jailAnchor(workingDir, processCwd string) string {
	if filepath.IsAbs(workingDir) {
		return filepath.Clean(workingDir)
	}
	return filepath.Clean(filepath.Join(processCwd, workingDir))
}

// landlockUnavailable decides the disposition of a G3 failure. Under require
// (the default, and every value that is not EXACTLY "prefer" — spec C9) it
// refuses with the unchanged pre-#648 error. Under prefer it degrades: the
// best-effort in-process tier is installed, Bash is not wrapped, and the
// caller gets the detail so it can emit jail_degraded (#648).
func landlockUnavailable(cfg *agent.SessionConfig, env *execpkg.LocalEnvironment, anchor string, globs []string, probeErr error) (jailSetup, error) {
	if cfg.WritablePathsMode != pipeline.WritablePathsModePrefer {
		return jailSetup{}, fmt.Errorf("writable_paths requires Landlock: %w", probeErr)
	}
	tier := installDegradedPolicy(env, anchor, globs)
	return jailSetup{Degraded: &pipeline.JailDegradedDetail{
		Mode: pipeline.WritablePathsModePrefer,
		// The in-process tier rides on the reason so the wire record says
		// which resolver bounded Write/Edit/ApplyPatch on this run.
		Reason:        fmt.Sprintf("%v; in-process tier: %s", probeErr, tier),
		DeclaredGlobs: append([]string(nil), globs...),
	}}, nil
}

// installEnforcedJail wires the full two-tier jail: Landlock for the Bash
// subprocess via __jail-exec, openat2-backed WriteOpener/Remover for the
// in-process tools. Unchanged from the pre-#648 configureJail body.
func installEnforcedJail(env *execpkg.LocalEnvironment, anchor string, globs []string) {
	env.CommandWrapper = func(c *osexec.Cmd) *osexec.Cmd {
		return execpkg.WrapBashCmd(c, anchor, globs)
	}
	installOpenat2InProcess(env, anchor, globs)
}

// installOpenat2InProcess wires the openat2-backed in-process tier
// (WriteOpener + Remover): glob policy, then symlink-safe SafeMkdirAll /
// OpenForWrite / SafeRemove against the anchor dirfd. Shared by the enforced
// jail and by the prefer degraded tier on a Linux host with openat2 but no
// Landlock ABI v3 (#648).
func installOpenat2InProcess(env *execpkg.LocalEnvironment, anchor string, globs []string) {
	env.WriteOpener = func(absPath string, perm os.FileMode) (*os.File, error) {
		relPath, err := jailPolicyCheck(anchor, absPath, globs)
		if err != nil {
			return nil, err
		}
		// Policy approved — create the parent dir via the symlink-safe
		// walker then open via openat2. SafeMkdirAll uses openat2 with
		// RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS on every intermediate
		// component so an agent who pre-creates "workspace" (or any
		// ancestor) as a symlink cannot redirect the MkdirAll outside
		// the jail (#275 review, Copilot codergen_jail.go:92). Order
		// matters: mkdir runs AFTER the glob check so a rejected write
		// leaves no empty directories.
		if err := execpkg.SafeMkdirAll(anchor, filepath.Dir(relPath), 0755); err != nil {
			return nil, err
		}
		return execpkg.OpenForWrite(anchor, relPath, perm)
	}
	env.Remover = func(absPath string) error {
		relPath, err := jailPolicyCheck(anchor, absPath, globs)
		if err != nil {
			return err
		}
		// SafeRemove resolves the parent dir via openat2 + unlinkat so
		// an agent who pre-creates any intermediate component as a
		// symlink cannot redirect the delete outside the jail (#275
		// review, Copilot codergen_jail.go:103).
		return execpkg.SafeRemove(anchor, relPath)
	}
}

// jailPolicyCheck is the glob policy shared by the enforced and degraded
// in-process tiers: absPath must sit beneath anchor (relPathForJail) and its
// anchor-relative form must match a declared glob. Returns the relative path
// on approval; ErrPathEscape / ErrPathNotAllowed otherwise.
func jailPolicyCheck(anchor, absPath string, globs []string) (string, error) {
	relPath, err := relPathForJail(anchor, absPath)
	if err != nil {
		return "", err
	}
	if !matchWritablePath(relPath, globs) {
		return "", fmt.Errorf("%w: %q does not match any writable_paths glob (%v)",
			execpkg.ErrPathNotAllowed, relPath, globs)
	}
	return relPath, nil
}

// installDegradedPolicy wires the in-process tier for a prefer node on a host
// without Landlock ABI v3 (#648, spec C6). Write/Edit/ApplyPatch (and the
// env-routed generate_code / write_enriched_sprint tools) stay bounded to the
// declared globs by the same policy check the enforced tier uses, and the
// strongest available symlink-safe resolver is kept:
//
//   - a Linux host with openat2 (kernel 5.6–6.1) reuses the enforced tier's
//     openat2 closures (RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS on every
//     component) — nothing is dropped "for symmetry";
//   - otherwise (macOS, Linux < 5.6) os.Root resolves every component
//     relative to the anchor and refuses any path that escapes it, so a
//     pre-planted symlink at an intermediate directory or at the leaf
//     (`anchor/link -> /outside`, `.git -> /outside`) cannot redirect a
//     write or a delete outside the anchor. Unlike openat2's
//     RESOLVE_NO_SYMLINKS, os.Root does follow a symlink that stays INSIDE
//     the anchor — the glob policy is evaluated on the lexical path, so such
//     an in-anchor link could land a write under a different in-anchor
//     directory than the glob named (documented residual, spec §7).
//
// env.CommandWrapper is deliberately NOT installed — the Bash subprocess is
// UNJAILED, and on a Linux host with Landlock ABI < 3 wrapping it would only
// make __jail-exec fail at ruleset creation. Returns the tier name for the
// degrade record.
func installDegradedPolicy(env *execpkg.LocalEnvironment, anchor string, globs []string) string {
	if execpkg.ProbeOpenat2() == nil {
		installOpenat2InProcess(env, anchor, globs)
		return "openat2"
	}
	installRootInProcess(env, anchor, globs)
	return "os.Root"
}

// relPathForJail validates that absPath sits beneath anchor and returns the
// relative path used by the jail's policy and openat2 layers. Rejects only
// real parent traversal: relPath == ".." or starts with "../" — bare names
// like "..foo" or "...cache" stay below the anchor and must be allowed
// through (#272 review, coderabbitai codergen_jail.go:82).
func relPathForJail(anchor, absPath string) (string, error) {
	relPath, relErr := filepath.Rel(anchor, absPath)
	if relErr != nil {
		return "", fmt.Errorf("%w: cannot relativize %q against anchor %q: %v",
			execpkg.ErrPathEscape, absPath, anchor, relErr)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q is outside anchor %q", execpkg.ErrPathEscape, absPath, anchor)
	}
	return relPath, nil
}

// matchWritablePath returns true when relPath matches any of the writable
// glob patterns. Supports:
//   - "**" — matches anything.
//   - "prefix/**" — matches the prefix itself and any descendant.
//   - "**/suffix" — matches at any depth, including the top level.
//   - "prefix/**/suffix" — matches "prefix/.../suffix" at any intermediate depth.
//   - "*.md", "foo/*.txt" — path.Match (single-segment globs).
//   - "exact/path.md" — literal match.
//
// All globs are workspace-relative and use forward-slash separators.
// Closes the #272 review gap (coderabbitai codergen_jail.go:129) where the
// previous matcher only honored trailing "/**" and bare "**".
func matchWritablePath(relPath string, globs []string) bool {
	for _, g := range globs {
		if matchOneGlob(relPath, g) {
			return true
		}
	}
	return false
}

func matchOneGlob(relPath, g string) bool {
	// Static glob: literal match.
	if !strings.ContainsAny(g, "*?[") {
		return relPath == g
	}
	if g == "**" {
		return true
	}
	if strings.Contains(g, "**") {
		// Split on the first "/**" boundary. Note: we deliberately don't
		// support multiple "**" in one glob — that's a doublestar feature
		// we don't need for the documented adopters.
		i := strings.Index(g, "/**")
		switch {
		case i == 0:
			// "/**suffix" — same shape as "**/suffix" after stripping the
			// leading "/". Treat both as "any-path-prefix + suffix".
			rest := strings.TrimPrefix(g[3:], "/")
			return rest == "" || matchSuffixAtAnyDepth(relPath, rest)
		case i < 0:
			// No "/**" present but contains "**" elsewhere (e.g. leading
			// "**/x"). Handle the "**/" prefix case here.
			if strings.HasPrefix(g, "**/") {
				return matchSuffixAtAnyDepth(relPath, strings.TrimPrefix(g, "**/"))
			}
			// Fallback: anything else with embedded ** is not supported.
			// path.Match doesn't understand **, so this would mis-match;
			// reject.
			return false
		default:
			// "prefix/**" or "prefix/**/suffix".
			prefix := g[:i]
			rest := strings.TrimPrefix(g[i+3:], "/")
			if !strings.HasPrefix(relPath+"/", prefix+"/") {
				return false
			}
			if rest == "" {
				return relPath == prefix || strings.HasPrefix(relPath, prefix+"/")
			}
			after := strings.TrimPrefix(relPath, prefix)
			after = strings.TrimPrefix(after, "/")
			return matchSuffixAtAnyDepth(after, rest)
		}
	}
	// Single-segment glob (*, ?, []). path.Match treats "/" as a separator.
	ok, _ := path.Match(g, relPath)
	return ok
}

// matchSuffixAtAnyDepth returns true when suffix matches relPath after
// trimming zero or more leading path components. Each trim step delegates
// to path.Match so single-segment globs in the suffix still work
// (e.g. matchSuffixAtAnyDepth("a/b/c.md", "*.md") = true via the "c.md" step).
func matchSuffixAtAnyDepth(relPath, suffix string) bool {
	for {
		if ok, _ := path.Match(suffix, relPath); ok {
			return true
		}
		idx := strings.Index(relPath, "/")
		if idx < 0 {
			return false
		}
		relPath = relPath[idx+1:]
	}
}
