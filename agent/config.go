// ABOUTME: Configuration for agent sessions including turn limits, timeouts, and loop detection.
// ABOUTME: Provides sensible defaults via DefaultConfig() and validation via Validate().
package agent

import (
	"fmt"
	"time"
)

type SessionConfig struct {
	MaxTurns               int
	CommandTimeout         time.Duration
	MaxCommandTimeout      time.Duration
	LoopDetectionThreshold int
	WorkingDir             string
	SystemPrompt           string
	Model                  string
	Provider               string
}

func DefaultConfig() SessionConfig {
	return SessionConfig{
		MaxTurns:               50,
		CommandTimeout:         10 * time.Second,
		MaxCommandTimeout:      10 * time.Minute,
		LoopDetectionThreshold: 10,
		WorkingDir:             ".",
	}
}

func (c SessionConfig) Validate() error {
	if c.MaxTurns < 1 {
		return fmt.Errorf("MaxTurns must be >= 1, got %d", c.MaxTurns)
	}
	if c.CommandTimeout < 0 {
		return fmt.Errorf("CommandTimeout must be >= 0, got %v", c.CommandTimeout)
	}
	if c.MaxCommandTimeout < 0 {
		return fmt.Errorf("MaxCommandTimeout must be >= 0, got %v", c.MaxCommandTimeout)
	}
	if c.LoopDetectionThreshold < 1 {
		return fmt.Errorf("LoopDetectionThreshold must be >= 1, got %d", c.LoopDetectionThreshold)
	}
	return nil
}
