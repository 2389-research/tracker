// ABOUTME: Windows stub for disk space check — syscall.Statfs not available.
//go:build windows

package tracker

func checkDiskSpaceLib(_ string) CheckResult {
	return CheckResult{
		Name:    "Disk Space",
		Status:  "ok",
		Message: "disk space check not available on Windows",
	}
}
