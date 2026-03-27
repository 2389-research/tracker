// ABOUTME: Non-blocking startup version check with 24h file-based cache.
// ABOUTME: Prints a one-line hint if an update is available, never blocks.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type updateCheckCache struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
}

// maybeCheckForUpdate runs a non-blocking version check in the background.
// If a newer version is available and the last check was >24h ago, prints a hint.
func maybeCheckForUpdate() {
	// Don't check in CI or non-interactive environments
	if os.Getenv("CI") != "" || os.Getenv("TRACKER_NO_UPDATE_CHECK") != "" {
		return
	}

	go func() {
		defer func() { recover() }() // never crash the main process

		cachePath := updateCheckCachePath()
		if cachePath == "" {
			return
		}

		// Read cache
		cache := readUpdateCache(cachePath)
		if time.Since(cache.LastCheck) < 24*time.Hour {
			// Cached and recent — show hint if update available
			if cache.Latest != "" && cache.Latest != version && cache.Latest != "v"+version {
				fmt.Fprintf(os.Stderr, "Update available: %s (run `tracker update`)\n", cache.Latest)
			}
			return
		}

		// Cache is stale — fetch fresh data
		release, err := fetchLatestRelease()
		if err != nil {
			return // network failure — silently skip
		}

		cache.LastCheck = time.Now()
		cache.Latest = release.TagName
		writeUpdateCache(cachePath, cache)

		if cache.Latest != version && cache.Latest != "v"+version {
			fmt.Fprintf(os.Stderr, "Update available: %s (run `tracker update`)\n", cache.Latest)
		}
	}()
}

func updateCheckCachePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(configDir, "2389", "tracker")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "update-check.json")
}

func readUpdateCache(path string) updateCheckCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return updateCheckCache{}
	}
	var cache updateCheckCache
	json.Unmarshal(data, &cache)
	return cache
}

func writeUpdateCache(path string, cache updateCheckCache) {
	data, _ := json.Marshal(cache)
	os.WriteFile(path, data, 0o600)
}
