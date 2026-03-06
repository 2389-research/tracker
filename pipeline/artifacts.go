package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type stageStatus struct {
	Outcome            string            `json:"outcome"`
	PreferredNextLabel string            `json:"preferred_next_label,omitempty"`
	SuggestedNextIDs   []string          `json:"suggested_next_ids,omitempty"`
	ContextUpdates     map[string]string `json:"context_updates,omitempty"`
}

func WriteStageArtifacts(rootDir, nodeID, prompt, response string, outcome Outcome) error {
	if rootDir == "" {
		return nil
	}
	stageDir := filepath.Join(rootDir, nodeID)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("create stage dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("write prompt artifact: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "response.md"), []byte(response), 0o644); err != nil {
		return fmt.Errorf("write response artifact: %w", err)
	}
	if err := WriteStatusArtifact(rootDir, nodeID, outcome); err != nil {
		return err
	}
	return nil
}

func WriteStatusArtifact(rootDir, nodeID string, outcome Outcome) error {
	if rootDir == "" {
		return nil
	}
	stageDir := filepath.Join(rootDir, nodeID)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("create stage dir: %w", err)
	}
	payload := stageStatus{
		Outcome:            outcome.Status,
		PreferredNextLabel: outcome.PreferredLabel,
		SuggestedNextIDs:   outcome.SuggestedNextNodes,
		ContextUpdates:     outcome.ContextUpdates,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status artifact: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "status.json"), data, 0o644); err != nil {
		return fmt.Errorf("write status artifact: %w", err)
	}
	return nil
}
