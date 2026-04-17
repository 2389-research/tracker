// ABOUTME: Windows stub for disk space check — syscall.Statfs not available.
//go:build windows

package tracker

func checkDiskSpace(_ string) CheckResult {
	return CheckResult{
		Name:    "Disk Space",
		Status:  CheckStatusOK,
		Message: "disk space check not available on Windows",
	}
}
