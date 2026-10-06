// ABOUTME: Source-aware .env loading for the tracker CLI (#659): the config .env may set credentials, base
// ABOUTME: URLs and knobs; the project .env may set provider keys only; the shell always wins and is never overwritten.
package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/2389-research/tracker/internal/envpolicy"
	"github.com/joho/godotenv"
)

// envFilesMode selects which .env files a command loads (--env-files / TRACKER_ENV_FILES).
type envFilesMode string

const (
	envFilesAll    envFilesMode = "all"
	envFilesConfig envFilesMode = "config"
	envFilesNone   envFilesMode = "none"
)

// envFilesStderr receives the one-line-per-key "ignoring" notices. Tests swap it.
var envFilesStderr io.Writer = os.Stderr

// loadEnvFilesMode loads the config .env and then <workdir>/.env according to
// the resolved mode (flag, then TRACKER_ENV_FILES, then "all"). It is the
// entry point for `run` and `doctor`.
func loadEnvFilesMode(workdir string, flagMode string) error {
	mode, err := resolveEnvFilesMode(flagMode)
	if err != nil {
		return err
	}
	return loadEnvFilesWithMode(workdir, mode)
}

// loadConfigEnvOnly loads at most the config .env. `tracker version` uses it:
// that command is run casually inside untrusted checkouts and must never pick
// up a project .env from the current directory.
func loadConfigEnvOnly(flagMode string) error {
	mode, err := resolveEnvFilesMode(flagMode)
	if err != nil {
		return err
	}
	if mode == envFilesAll {
		mode = envFilesConfig
	}
	return loadEnvFilesWithMode("", mode)
}

// resolveEnvFilesMode picks the mode: an explicit flag wins, then the
// shell-only TRACKER_ENV_FILES, then the default "all". An unknown value is
// an error rather than a silent fallback.
func resolveEnvFilesMode(flagMode string) (envFilesMode, error) {
	raw, origin := flagMode, "--env-files"
	if raw == "" {
		raw, origin = os.Getenv("TRACKER_ENV_FILES"), "TRACKER_ENV_FILES"
	}
	switch envFilesMode(raw) {
	case "":
		return envFilesAll, nil
	case envFilesAll, envFilesConfig, envFilesNone:
		return envFilesMode(raw), nil
	}
	return "", fmt.Errorf("invalid %s=%q: must be one of all, config, none", origin, raw)
}

// validateEnvFilesFlag rejects a bad --env-files value at flag-parse time.
func validateEnvFilesFlag(value string) error {
	if value == "" {
		return nil
	}
	_, err := resolveEnvFilesMode(value)
	return err
}

func loadEnvFilesWithMode(workdir string, mode envFilesMode) error {
	envpolicy.ResetProvenance()
	if mode == envFilesNone {
		return nil
	}
	originalEnv := currentEnvKeys()

	configEnvPath, err := resolveConfigEnvPath()
	if err != nil {
		return fmt.Errorf("resolve XDG config dir: %w", err)
	}
	if err := loadEnvFileIfPresent(configEnvPath, envpolicy.ConfigEnv, originalEnv); err != nil {
		return err
	}
	if mode == envFilesConfig {
		return nil
	}
	return loadEnvFileIfPresent(filepath.Join(workdir, ".env"), envpolicy.ProjectEnv, originalEnv)
}

func currentEnvKeys() map[string]struct{} {
	keys := make(map[string]struct{})
	for _, entry := range os.Environ() {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			keys[entry[:idx]] = struct{}{}
		}
	}
	return keys
}

func loadEnvFileIfPresent(path string, src envpolicy.Source, originalEnv map[string]struct{}) error {
	values, ok, err := readEnvFileSecurely(path, src)
	if err != nil || !ok {
		return err
	}
	return applyEnvValues(path, src, values, originalEnv)
}

// readEnvFileSecurely reads a .env file with O_NOFOLLOW semantics. It returns
// (values, true, nil) on success, (nil, false, nil) when the file is absent or
// refused on hygiene grounds (symlink; group/world-writable project file — a
// notice is printed), and an error when the file exists but cannot be read.
func readEnvFileSecurely(path string, src envpolicy.Source) (map[string]string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("stat env file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		envNotice("%s: ignoring this .env file: it is a symlink (put the real file here instead)", path)
		return nil, false, nil
	}
	if src == envpolicy.ProjectEnv && looseEnvFileMode(info.Mode()) {
		envNotice("%s: ignoring this .env file: it is group/world-writable (mode %04o); chmod 600 it", path, info.Mode().Perm())
		return nil, false, nil
	}
	f, err := openEnvFileNoFollow(path)
	if err != nil {
		return nil, false, fmt.Errorf("open env file %s: %w", path, err)
	}
	defer f.Close()
	values, err := godotenv.Parse(f)
	if err != nil {
		return nil, false, fmt.Errorf("load env file %s: %w", path, err)
	}
	return values, true, nil
}

// looseEnvFileMode reports whether anyone but the owner may write the file.
// Permission bits carry no meaning on Windows, so the check is skipped there.
func looseEnvFileMode(mode os.FileMode) bool {
	return runtime.GOOS != "windows" && mode.Perm()&0o022 != 0
}

// applyEnvValues sets the variables a file of tier src is allowed to set,
// in name order so the notices are deterministic. A name already present in
// the shell environment is skipped silently (shell wins); a name the tier may
// not set, or that Tracker does not read at all, is skipped with one stderr
// line and recorded for doctor.
func applyEnvValues(path string, src envpolicy.Source, values map[string]string, originalEnv map[string]struct{}) error {
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if _, exists := originalEnv[key]; exists {
			continue
		}
		if !envpolicy.AllowedFrom(key, src) {
			reason := envRefusalReason(key, src)
			envNotice("%s: ignoring %s — %s", path, key, reason)
			envpolicy.RecordSkipped(key, path, reason)
			continue
		}
		if err := os.Setenv(key, values[key]); err != nil {
			return fmt.Errorf("set env %s from %s: %w", key, path, err)
		}
		envpolicy.RecordApplied(key, path)
	}
	return nil
}

// envRefusalReason explains why a file of tier src may not set key.
func envRefusalReason(key string, src envpolicy.Source) string {
	v, ok := envpolicy.Lookup(key)
	if !ok {
		return "not a variable tracker reads; a .env file may only supply registered names (export it in the shell if a subprocess needs it)"
	}
	return fmt.Sprintf("%s: allowed from %s, not from this %s file",
		strings.ReplaceAll(string(v.Purpose), "_", " "), v.Source.Describe(), src)
}

func envNotice(format string, args ...any) {
	fmt.Fprintf(envFilesStderr, "tracker: "+format+"\n", args...)
}
