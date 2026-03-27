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

// updateHintCh delivers the update hint (if any) from the background goroutine
// to the main goroutine, preventing stderr races with TUI or other output.
// The goroutine always closes this channel when done, so printUpdateHint
// returns immediately in the common case (no update / cached).
var updateHintCh = make(chan string, 1)

// maybeCheckForUpdate runs a non-blocking version check in the background.
// If a newer version is available, sends the version to updateHintCh.
// Always closes updateHintCh when done so printUpdateHint doesn't block.
func maybeCheckForUpdate() {
	if version == "dev" {
		close(updateHintCh)
		return
	}
	if os.Getenv("CI") != "" || os.Getenv("TRACKER_NO_UPDATE_CHECK") != "" {
		close(updateHintCh)
		return
	}

	go func() {
		defer close(updateHintCh)
		// Recover panics silently — writing to stderr here would corrupt
		// the TUI if BubbleTea has taken over the terminal.
		defer func() { recover() }()

		cachePath := updateCheckCachePath()
		if cachePath == "" {
			return
		}

		cache := readUpdateCache(cachePath)
		if time.Since(cache.LastCheck) < 24*time.Hour {
			if cache.Latest != "" && !versionsEqual(cache.Latest, version) {
				updateHintCh <- cache.Latest
			}
			return
		}

		// Cache is stale — fetch fresh data.
		// This HTTP call typically takes 200-2000ms. printUpdateHint waits
		// up to 2s for it, but on the first stale check the hint may be
		// missed. The cache update still persists so the next run shows it.
		release, err := fetchLatestRelease()
		if err != nil {
			return // network failure — silently skip
		}

		cache.LastCheck = time.Now()
		cache.Latest = release.TagName
		writeUpdateCache(cachePath, cache)

		if !versionsEqual(cache.Latest, version) {
			updateHintCh <- cache.Latest
		}
	}()
}

// printUpdateHint drains the update hint channel. Blocks until the background
// goroutine finishes (channel closed) or 2s, whichever comes first.
// In the common case (cache hit, no update), this returns instantly because
// the goroutine closes the channel immediately.
func printUpdateHint() {
	select {
	case latest, ok := <-updateHintCh:
		if ok && latest != "" {
			fmt.Fprintf(os.Stderr, "Update available: %s → %s (run `tracker update`)\n", version, latest)
		}
	case <-time.After(2 * time.Second):
		// Don't block shutdown waiting for a slow network fetch
	}
}

func updateCheckCachePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(configDir, "2389", "tracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return filepath.Join(dir, "update-check.json")
}

func readUpdateCache(path string) updateCheckCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return updateCheckCache{}
	}
	var cache updateCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		os.Remove(path) // corrupt cache, start fresh
		return updateCheckCache{}
	}
	return cache
}

func writeUpdateCache(path string, cache updateCheckCache) {
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0o600)
}
