// ABOUTME: Configuration for agent sessions including turn limits, timeouts, and loop detection.
// ABOUTME: Provides sensible defaults via DefaultConfig() and validation via Validate().
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CompactionMode string

const (
	CompactionNone CompactionMode = "none"
	CompactionAuto CompactionMode = "auto"
)

type SessionConfig struct {
	MaxTurns                      int
	CommandTimeout                time.Duration
	MaxCommandTimeout             time.Duration
	LoopDetectionThreshold        int
	ContextWindowLimit            int
	ContextWindowWarningThreshold float64
	ToolOutputLimits              map[string]int
	WorkingDir                    string
	SystemPrompt                  string
	Model                         string
	Provider                      string
	CacheToolResults              bool
	ContextCompaction             CompactionMode
	CompactionThreshold           float64
	ReasoningEffort               string // OpenAI reasoning effort: "low", "medium", "high"
	ResponseFormat                string // "json_object" or "json_schema" — forces structured output
	ResponseSchema                string // JSON schema string when ResponseFormat is "json_schema"
	// ReflectOnError injects a structured reflection prompt after tool call
	// errors to help the LLM reason about what went wrong before retrying.
	// Default: true.
	ReflectOnError bool

	// VerifyAfterEdit enables automatic test/lint verification after turns that
	// include file writes or edits. If verification fails, the error is fed back
	// to the LLM with a repair prompt. Default: false (opt-in).
	VerifyAfterEdit bool

	// VerifyCommand is the explicit verification command to run. When empty,
	// auto-detection is used (looks for go.mod → "go test ./...", Cargo.toml →
	// "cargo test", package.json → "npm test", Makefile with test target →
	// "make test", pytest markers → "pytest").
	VerifyCommand string

	// MaxVerifyRetries is the maximum number of verify→repair cycles per edit
	// turn before giving up and proceeding. Default: 2.
	MaxVerifyRetries int

	// VerifyOnBreach, when true, makes the session run one verify pass after
	// the turn loop exhausts (MaxTurns reached without a detected loop), using
	// VerifyCommand only (never auto-detection — see resolveBreachVerifier).
	// The pipeline layer sets this to (turn_breach_policy != "fail") so the
	// opt-out path pays no verify cost. Independent of VerifyAfterEdit. (#303)
	VerifyOnBreach bool

	// Checkpoints are messages injected at specific turn-budget fractions.
	// Each checkpoint fires exactly once, on the turn where the fraction is
	// first reached. Fraction is in [0, 1] — e.g. 0.6 means "at 60% of MaxTurns".
	Checkpoints []Checkpoint

	// VerifyBroadCommand is an optional second verification command run after
	// the focused VerifyCommand passes. Use this for regression detection
	// (e.g. run the full test module without -x). Empty means disabled.
	VerifyBroadCommand string

	// Localize enables a pre-processing localization phase that scans the
	// working directory for files relevant to the task prompt and injects a
	// structured context block before the first LLM turn. Pure text analysis
	// plus filesystem scan — no LLM calls. Default: false.
	Localize bool

	// PriorEpisodeSummaries carries summaries from earlier attempts so retries
	// can avoid repeating known-failing approaches.
	PriorEpisodeSummaries []string
	// PlanBeforeExecute inserts one planning-only LLM call before the main turn
	// loop and keeps that plan in conversation context for subsequent turns.
	// Default: false.
	PlanBeforeExecute bool

	// ToolAccess restricts the agent's tool surface. When non-empty (any value),
	// the session registers zero tools, sets ToolChoice=none on LLM requests,
	// scrubs the built-in tool-naming prefix from the system prompt, and rejects
	// Params bypass keys (allowed_tools, disallowed_tools, permission_mode).
	//
	// Defends the v0.28.2 single-agent multi-tool-call vector: an LLM emitting
	// multiple tool calls in one response cannot execute any of them because
	// the registry is empty by construction.
	//
	// Canonical: case-insensitive, whitespace-trimmed. Only recognized spelling
	// is "none"; any other non-empty value still disables tools (fail-closed for
	// typos). Default: "" (unrestricted).
	//
	// System-prompt scope: tracker only scrubs its own built-in basePrompt
	// (which names "read", "write", etc. for path-relative semantics). A
	// caller-supplied SystemPrompt is appended verbatim — if it names tools,
	// the assembled prompt will still contain those tokens. The registry +
	// ToolChoice + dispatch-shortcircuit defenses do not depend on the prompt
	// scrub; the scrub is defense-in-depth against the LLM noticing tool
	// affordances. Callers who need a fully scrubbed assembled prompt should
	// audit their own SystemPrompt under restriction.
	//
	// Issue: github.com/2389-research/tracker#258.
	ToolAccess string

	// WritablePaths is the author-declared write-scope glob list resolved
	// against WorkingDir. Empty/absent = unbounded; non-empty = jail enforced
	// by the runtime (Linux Landlock for Bash subprocess + openat2 for
	// in-process tools). Empty values, malformed globs, working_dir escapes,
	// unsupported backends, and Landlock-unavailable hosts all refuse-to-start
	// at session creation via pipeline/handlers/codergen_jail.go's
	// configureJail gate (Task 14). See issue #272.
	WritablePaths []string

	// WritablePathsSet records whether the writable_paths attr was specified
	// on the originating node, even if the parsed slice is empty. Allows
	// configureJail to distinguish "absent" (Set=false, jail disabled) from
	// "present but parses to no entries" (Set=true, fail-CLOSED). Mirrors
	// pipeline.AgentNodeConfig.WritablePathsSet so the signal carries
	// through the codergen buildConfig handoff intact.
	WritablePathsSet bool

	// Backend names the execution backend for this session. Carried from
	// pipeline.AgentNodeConfig.Backend so configureJail can refuse
	// out-of-process backends (claude-code, acp) and unknown backends
	// (fail-closed) before wiring the writable_paths fs-jail. Empty string
	// is treated as "native" by configureJail. See issue #272.
	Backend string

	// MaxCostUSD is the per-node cumulative cost ceiling in USD. The session
	// halts after any turn whose cumulative cost exceeds this value and sets
	// SessionResult.NodeCostExceeded. Zero means no limit. (#304)
	MaxCostUSD float64

	// NoProgressTurns is the number of consecutive turns with no tool calls
	// after which the session halts and sets SessionResult.NoProgressDetected.
	// Zero means the detector is disabled. (#304)
	NoProgressTurns int
}

// IsToolAccessRestricted reports whether ToolAccess is set to any non-empty
// canonical value. Used by the session to gate tool registration, ToolChoice,
// and system-prompt assembly. Fail-closed: any non-empty value (including
// typos) returns true.
func (c SessionConfig) IsToolAccessRestricted() bool {
	return strings.TrimSpace(c.ToolAccess) != ""
}

// Checkpoint defines a message to inject at a specific turn-budget fraction.
type Checkpoint struct {
	Fraction float64 // 0.0–1.0 fraction of MaxTurns
	Message  string  // message injected as a user message
}

const (
	DefaultModel    = "claude-sonnet-4-6"
	DefaultProvider = "anthropic"
)

func DefaultConfig() SessionConfig {
	return SessionConfig{
		MaxTurns:                      80,
		CommandTimeout:                10 * time.Second,
		MaxCommandTimeout:             10 * time.Minute,
		LoopDetectionThreshold:        4,
		ContextWindowLimit:            200000,
		ContextWindowWarningThreshold: 0.8,
		WorkingDir:                    ".",
		Model:                         DefaultModel,
		Provider:                      DefaultProvider,
		ContextCompaction:             CompactionNone,
		ReflectOnError:                true,
		MaxVerifyRetries:              2,
	}
}

func (c SessionConfig) Validate() error {
	if err := c.validateTimeouts(); err != nil {
		return err
	}
	if err := c.validateLimits(); err != nil {
		return err
	}
	if err := c.validateToolOutputLimits(); err != nil {
		return err
	}
	if err := c.validateCheckpoints(); err != nil {
		return err
	}
	return c.validateResponseFormat()
}

// validateCheckpoints checks that all checkpoint fractions are in [0, 1] and messages are non-empty.
func (c SessionConfig) validateCheckpoints() error {
	for i, cp := range c.Checkpoints {
		if cp.Fraction < 0 || cp.Fraction > 1 {
			return fmt.Errorf("Checkpoints[%d].Fraction must be in [0, 1], got %f", i, cp.Fraction)
		}
		if cp.Message == "" {
			return fmt.Errorf("Checkpoints[%d].Message must be non-empty", i)
		}
	}
	return nil
}

// validateTimeouts checks turn, command timeout, and loop detection fields.
func (c SessionConfig) validateTimeouts() error {
	if c.MaxTurns < 1 {
		return fmt.Errorf("MaxTurns must be >= 1, got %d", c.MaxTurns)
	}
	if c.CommandTimeout <= 0 {
		return fmt.Errorf("CommandTimeout must be > 0, got %v", c.CommandTimeout)
	}
	if c.MaxCommandTimeout <= 0 {
		return fmt.Errorf("MaxCommandTimeout must be > 0, got %v", c.MaxCommandTimeout)
	}
	if c.MaxCommandTimeout < c.CommandTimeout {
		return fmt.Errorf("MaxCommandTimeout (%v) must be >= CommandTimeout (%v)", c.MaxCommandTimeout, c.CommandTimeout)
	}
	if c.LoopDetectionThreshold < 1 {
		return fmt.Errorf("LoopDetectionThreshold must be >= 1, got %d", c.LoopDetectionThreshold)
	}
	return nil
}

// validateLimits checks context window and compaction threshold fields.
func (c SessionConfig) validateLimits() error {
	if c.ContextWindowLimit < 1000 {
		return fmt.Errorf("ContextWindowLimit must be >= 1000, got %d", c.ContextWindowLimit)
	}
	if c.ContextWindowWarningThreshold <= 0 || c.ContextWindowWarningThreshold > 1.0 {
		return fmt.Errorf("ContextWindowWarningThreshold must be > 0 and <= 1.0, got %f", c.ContextWindowWarningThreshold)
	}
	if c.ContextCompaction == CompactionAuto {
		if c.CompactionThreshold <= 0 || c.CompactionThreshold > 1.0 {
			return fmt.Errorf("CompactionThreshold must be > 0 and <= 1.0 when compaction is auto, got %f", c.CompactionThreshold)
		}
	}
	if c.MaxVerifyRetries < 0 {
		return fmt.Errorf("MaxVerifyRetries must be >= 0, got %d", c.MaxVerifyRetries)
	}
	return nil
}

// validateToolOutputLimits checks all per-tool output limit values.
func (c SessionConfig) validateToolOutputLimits() error {
	for name, limit := range c.ToolOutputLimits {
		if limit <= 0 {
			return fmt.Errorf("ToolOutputLimits[%q] must be > 0, got %d", name, limit)
		}
	}
	return nil
}

// validateResponseFormat checks ResponseFormat and ResponseSchema consistency.
func (c SessionConfig) validateResponseFormat() error {
	if c.ResponseFormat == "" {
		return nil
	}
	if c.ResponseFormat != "json_object" && c.ResponseFormat != "json_schema" {
		return fmt.Errorf("ResponseFormat must be \"json_object\" or \"json_schema\", got %q", c.ResponseFormat)
	}
	if c.ResponseFormat == "json_schema" {
		if c.ResponseSchema == "" {
			return fmt.Errorf("ResponseSchema must be non-empty when ResponseFormat is \"json_schema\"")
		}
		if !json.Valid([]byte(c.ResponseSchema)) {
			return fmt.Errorf("ResponseSchema must be valid JSON, got %q", c.ResponseSchema)
		}
	}
	return nil
}
