package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/2389-research/tracker/internal/envpolicy"
	"github.com/joho/godotenv"
)

// providerEnvKeys is the set of names `tracker setup` may write to the config
// .env: every provider credential plus every per-provider base URL, taken
// from the env registry so the wizard and the loader cannot disagree (#659).
var providerEnvKeys = buildProviderEnvKeys()

func buildProviderEnvKeys() map[string]struct{} {
	keys := make(map[string]struct{})
	for _, n := range envpolicy.ProviderKeyVars() {
		keys[n] = struct{}{}
	}
	for _, n := range envpolicy.ProviderBaseURLVars() {
		keys[n] = struct{}{}
	}
	return keys
}

func resolveConfigEnvPath() (string, error) {
	configHome, err := xdgConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, "tracker", ".env"), nil
}

// xdgConfigHome returns $XDG_CONFIG_HOME when it is an absolute path, else
// $HOME/.config. A relative value is ignored with a notice (like
// pipeline.absEnv) so the config .env can never resolve under the current
// directory of an untrusted checkout.
func xdgConfigHome() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		if filepath.IsAbs(dir) {
			return dir, nil
		}
		envNotice("ignoring relative XDG_CONFIG_HOME=%q (must be absolute); using $HOME/.config", dir)
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
