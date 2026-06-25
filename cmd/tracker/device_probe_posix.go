//go:build !windows

// ABOUTME: POSIX real implementation of the /dev/null device probe (#423).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// probeDevNull verifies /dev/null is a usable character device that is both
// readable and writable. Catches the corruption class behind #423: a node that
// became unreadable, a regular file masquerading as the device, or a vanished
// path — any of which silently breaks git and subprocess handlers downstream.
func probeDevNull() error {
	const path = "/dev/null"
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s is not a character device (mode %v)", path, fi.Mode())
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s O_RDWR: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write([]byte{0}); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	buf := make([]byte, 1)
	if _, err := f.Read(buf); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}
