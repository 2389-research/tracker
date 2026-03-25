// ABOUTME: Setup wizard command — interactive provider configuration UI.
// ABOUTME: Reads/writes API keys and base URLs to the XDG config .env file.
package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func runSetup() error {
	return runSetupCommand(runSetupUI)
}

func runSetupCommand(runUI func(existing map[string]string) (setupResult, error)) error {
	configPath, err := resolveConfigEnvPath()
	if err != nil {
		return fmt.Errorf("resolve XDG config dir: %w", err)
	}

	existing, err := readEnvFile(configPath)
	if err != nil {
		return err
	}

	result, err := runUI(existing)
	if err != nil {
		return err
	}
	if result.cancelled {
		return nil
	}

	merged := mergeProviderEnv(existing, result.values)
	if envMapsEqual(existing, merged) {
		return nil
	}

	return writeEnvFile(configPath, merged)
}

func runSetupUI(existing map[string]string) (setupResult, error) {
	model := newSetupModel(existing)
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return setupResult{}, err
	}

	final, ok := finalModel.(setupModel)
	if !ok {
		return setupResult{}, fmt.Errorf("unexpected setup model type %T", finalModel)
	}

	return setupResult{
		values:    final.pendingUpdates(),
		cancelled: final.cancelled,
	}, nil
}
