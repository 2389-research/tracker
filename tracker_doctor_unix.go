// ABOUTME: Unix-specific disk space check using syscall.Statfs.
//go:build !windows

package tracker

import (
	"fmt"
	"syscall"
)

func checkDiskSpaceLib(workdir string) CheckResult {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(workdir, &stat); err != nil {
		return CheckResult{
			Name:    "Disk Space",
			Status:  "warn",
			Message: fmt.Sprintf("could not determine disk space: %v", err),
		}
	}
	available := stat.Bavail * uint64(stat.Bsize)
	availableGB := float64(available) / (1024 * 1024 * 1024)
	const minGB = 10.0
	if availableGB < minGB {
		return CheckResult{
			Name:    "Disk Space",
			Status:  "warn",
			Message: fmt.Sprintf("low disk space: %.2f GB available (recommended: %.1f GB+)", availableGB, minGB),
			Hint:    "free up disk space before running long pipelines",
		}
	}
	return CheckResult{
		Name:    "Disk Space",
		Status:  "ok",
		Message: fmt.Sprintf("%.2f GB available", availableGB),
	}
}
