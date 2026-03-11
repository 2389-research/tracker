package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

var providerEnvKeys = map[string]struct{}{
	"OPENAI_API_KEY":    {},
	"ANTHROPIC_API_KEY": {},
	"GEMINI_API_KEY":    {},
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

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(values[key])
		b.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write env file %s: %w", path, err)
	}
	return nil
}
