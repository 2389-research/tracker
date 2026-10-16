// ABOUTME: Setup wizard command — interactive provider configuration UI.
// ABOUTME: Reads/writes API keys, base URLs and the OpenAI auth switch in the XDG config .env file.
package main

import (
	"fmt"
	"maps"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/internal/chatgptauth"
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
	if maps.Equal(existing, merged) {
		return nil
	}

	return writeEnvFile(configPath, merged)
}

func runSetupUI(existing map[string]string) (setupResult, error) {
	model, err := newSetupUIModel(existing)
	if err != nil {
		return setupResult{}, err
	}
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return setupResult{}, err
	}

	final, ok := finalModel.(setupModel)
	if !ok {
		return setupResult{}, fmt.Errorf("unexpected setup model type %T", finalModel)
	}

	return setupResult{
		values:    final.collectValues(),
		cancelled: final.cancelled,
	}, nil
}

// newSetupUIModel builds the model for a real run: the ChatGPT sign-in goes
// through the real auth client and saves to the token file's default path,
// whose status the OpenAI page shows.
func newSetupUIModel(existing map[string]string) (setupModel, error) {
	authPath, err := chatgptauth.DefaultPath()
	if err != nil {
		return setupModel{}, fmt.Errorf("resolve the ChatGPT sign-in path: %w", err)
	}
	return newSetupModel(existing).withChatGPTSignin(chatgptauth.NewAuthClient(), authPath), nil
}
