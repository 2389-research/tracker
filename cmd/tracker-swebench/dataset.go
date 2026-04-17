// ABOUTME: SWE-bench Lite JSONL dataset parser for the tracker-swebench harness.
// ABOUTME: Provides LoadDataset to read instances from a JSONL file and Instance methods for prompt generation.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Instance represents a single SWE-bench Lite benchmark task.
type Instance struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	HintsText        string `json:"hints_text"`
	Version          string `json:"version"`
	EnvSetupCommit   string `json:"environment_setup_commit"`
}

// RepoURL returns the GitHub clone URL for this instance's repository.
func (inst Instance) RepoURL() string {
	return "https://github.com/" + inst.Repo + ".git"
}

// AgentPrompt returns the problem statement, appending hints if present.
func (inst Instance) AgentPrompt() string {
	if inst.HintsText == "" {
		return inst.ProblemStatement
	}
	return inst.ProblemStatement + "\n\n## Hints\n\n" + inst.HintsText
}

// instanceIDPattern matches valid SWE-bench instance IDs: alphanumeric, underscores, hyphens, dots.
var instanceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.=-]*$`)

// validateInstanceID checks that an instance ID is safe for use as a Docker
// container name suffix and filesystem path component.
func validateInstanceID(id string) error {
	if id == "" {
		return fmt.Errorf("empty instance ID")
	}
	if !instanceIDPattern.MatchString(id) {
		return fmt.Errorf("invalid instance ID %q: must match [a-zA-Z0-9][a-zA-Z0-9_.=-]*", id)
	}
	return nil
}

// LoadDataset reads a JSONL file and returns the parsed instances.
// Blank lines are skipped. Returns an error with the line number on malformed JSON.
func LoadDataset(path string) ([]Instance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dataset: %w", err)
	}
	defer f.Close()

	// Use a 10MB scanner buffer to handle large lines.
	const maxBuf = 10 * 1024 * 1024
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, maxBuf), maxBuf)

	var instances []Instance
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var inst Instance
		if err := json.Unmarshal([]byte(line), &inst); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		if err := validateInstanceID(inst.InstanceID); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		instances = append(instances, inst)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan dataset: %w", err)
	}

	return instances, nil
}
