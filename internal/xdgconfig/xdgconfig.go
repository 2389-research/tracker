// ABOUTME: Resolves the XDG config-home directory ($XDG_CONFIG_HOME or
// ABOUTME: $HOME/.config) shared by the CLI and the chatgptauth token-file reader.
package xdgconfig

import (
	"os"
	"path/filepath"
)

// ConfigHome returns $XDG_CONFIG_HOME when that is an absolute path, else
// $HOME/.config. A relative $XDG_CONFIG_HOME is never used to build dir;
// it is returned as ignored so a caller can warn about it. ignored is ""
// when there was nothing to skip. ConfigHome itself never prints.
func ConfigHome() (dir, ignored string, err error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		if filepath.IsAbs(v) {
			return v, "", nil
		}
		ignored = v
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", ignored, err
	}
	return filepath.Join(home, ".config"), ignored, nil
}
