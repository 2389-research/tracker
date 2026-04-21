// ABOUTME: Configuration for agent sessions including turn limits, timeouts, and loop detection.
// ABOUTME: Provides sensible defaults via DefaultConfig() and validation via Validate().
package agent

import (
	"encoding/json"
	"fmt"
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
}

// Checkpoint defines a message to inject at a specific turn-budget fraction.
type Checkpoint struct {
	Fraction float64 // 0.0–1.0 fraction of MaxTurns
	Message  string  // message injected as a user message
}

const (
	DefaultModel    = "claude-sonnet-4-5"
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
