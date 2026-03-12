package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

var providerEnvKeys = map[string]struct{}{
	"OPENAI_API_KEY":     {},
	"ANTHROPIC_API_KEY":  {},
	"GEMINI_API_KEY":     {},
	"GOOGLE_API_KEY":     {},
	"OPENAI_BASE_URL":    {},
	"ANTHROPIC_BASE_URL": {},
	"GEMINI_BASE_URL":    {},
}

func resolveConfigEnvPath() (string, error) {
	configHome, err := xdgConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, "tracker", ".env"), nil
}

func xdgConfigHome() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

func readEnvFile(path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("stat env file %s: %w", path, err)
	}

	values, err := godotenv.Read(path)
	if err != nil {
		return nil, fmt.Errorf("load env file %s: %w", path, err)
	}
	return values, nil
}

func mergeProviderEnv(existing, updates map[string]string) map[string]string {
	merged := make(map[string]string, len(existing))
	for key, value := range existing {
		merged[key] = value
	}

	for key, value := range updates {
		if _, ok := providerEnvKeys[key]; !ok {
			continue
		}
		if value == "" {
			continue
		}
		merged[key] = value
	}

	return merged
}

func writeEnvFile(path string, values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir config dir for %s: %w", path, err)
	}

	content, err := godotenv.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal env values: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write env file %s: %w", path, err)
	}
	return nil
}
