// ABOUTME: Configuration for agent sessions including turn limits, timeouts, and loop detection.
// ABOUTME: Provides sensible defaults via DefaultConfig() and validation via Validate().
package agent

import (
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
	CacheToolResults    bool
	ContextCompaction   CompactionMode
	CompactionThreshold float64
}

const (
	DefaultModel    = "claude-sonnet-4-5"
	DefaultProvider = "anthropic"
)

func DefaultConfig() SessionConfig {
	return SessionConfig{
		MaxTurns:                      50,
		CommandTimeout:                10 * time.Second,
		MaxCommandTimeout:             10 * time.Minute,
		LoopDetectionThreshold:        10,
		ContextWindowLimit:            200000,
		ContextWindowWarningThreshold: 0.8,
		WorkingDir:                    ".",
		Model:                         DefaultModel,
		Provider:                      DefaultProvider,
		ContextCompaction:             CompactionNone,
	}
}

func (c SessionConfig) Validate() error {
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
	for name, limit := range c.ToolOutputLimits {
		if limit <= 0 {
			return fmt.Errorf("ToolOutputLimits[%q] must be > 0, got %d", name, limit)
		}
	}
	return nil
}
