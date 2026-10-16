// ABOUTME: Tests for the shared XDG config-home resolver.
// ABOUTME: Covers an absolute override, a relative override being ignored, and the unset-HOME fallback.
package xdgconfig

import (
	"path/filepath"
	"testing"
)

func TestConfigHomeAbsoluteXDG(t *testing.T) {
	abs := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", abs)

	dir, ignored, err := ConfigHome()
	if err != nil {
		t.Fatalf("ConfigHome returned error: %v", err)
	}
	if dir != abs {
		t.Errorf("dir = %q, want %q", dir, abs)
	}
	if ignored != "" {
		t.Errorf("ignored = %q, want empty", ignored)
	}
}

func TestConfigHomeRelativeXDGIgnored(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "rel/dir")

	dir, ignored, err := ConfigHome()
	if err != nil {
		t.Fatalf("ConfigHome returned error: %v", err)
	}
	want := filepath.Join(tmp, ".config")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if ignored != "rel/dir" {
		t.Errorf("ignored = %q, want %q", ignored, "rel/dir")
	}
}

func TestConfigHomeUnsetUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	dir, ignored, err := ConfigHome()
	if err != nil {
		t.Fatalf("ConfigHome returned error: %v", err)
	}
	want := filepath.Join(home, ".config")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if ignored != "" {
		t.Errorf("ignored = %q, want empty", ignored)
	}
}
