// ABOUTME: Doctor checks for the working directory, .gitignore hygiene, and artifact dirs.
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkWorkdir verifies the working directory exists and is writable.
// It also detects missing .gitignore entries but does NOT modify the file —
// the CLI applies any fix-up separately.
func checkWorkdir(workdir string) CheckResult {
	out := CheckResult{Name: "Working Directory"}
	info, err := os.Stat(workdir)
	if err != nil {
		return workdirStatError(out, workdir, err)
	}
	if !info.IsDir() {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s is not a directory", workdir)
		out.Hint = "point --workdir at a directory, not a file"
		return out
	}
	if !isDirWritable(workdir) {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s is not writable", workdir)
		out.Hint = fmt.Sprintf("check permissions: chmod u+w %s", workdir)
		return out
	}
	return workdirWarnings(out, workdir)
}

// workdirStatError maps an os.Stat failure on the working directory to an
// error result with path-specific guidance.
func workdirStatError(out CheckResult, workdir string, err error) CheckResult {
	out.Status = CheckStatusError
	switch {
	case os.IsNotExist(err):
		out.Message = fmt.Sprintf("%s does not exist", workdir)
		out.Hint = fmt.Sprintf("create the directory: mkdir -p %s", workdir)
	case os.IsPermission(err):
		out.Message = fmt.Sprintf("permission denied accessing %s", workdir)
		out.Hint = fmt.Sprintf("check permissions on %s or a parent directory", workdir)
	default:
		out.Message = fmt.Sprintf("cannot stat %s: %v", workdir, err)
		out.Hint = "check the path and its parent directories"
	}
	return out
}

// workdirWarnings appends non-fatal advisories (home/root location, missing
// .gitignore entries) for a writable working directory and sets the summary.
func workdirWarnings(out CheckResult, workdir string) CheckResult {
	hasWarn := false
	home, _ := os.UserHomeDir()
	if workdir == home || workdir == "/" {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: fmt.Sprintf("%s (risk of accidental data loss — use a project subdirectory)", workdir),
		})
		hasWarn = true
	}

	// Detect missing .gitignore entries without modifying the file.
	if missing := missingGitignoreEntries(workdir); missing != "" {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: missing,
		})
		hasWarn = true
	}

	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("%s (writable)", workdir),
	})
	if hasWarn {
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("%s is writable (with warnings)", workdir)
	} else {
		out.Status = CheckStatusOK
		out.Message = fmt.Sprintf("%s is writable", workdir)
	}
	return out
}

// missingGitignoreEntries returns a warning message if .gitignore is missing
// or lacks .tracker/, runs/, or .ai/ entries. Returns empty string if OK.
// Read-only — does not modify the file.
func missingGitignoreEntries(workdir string) string {
	gitignorePath := filepath.Join(workdir, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		return ".gitignore not found — add .tracker/, runs/, and .ai/ to prevent committing run artifacts"
	}
	entries := parseGitignoreEntries(string(content))
	want := []struct {
		bare    string
		display string
	}{
		{".tracker", ".tracker/"},
		{"runs", "runs/"},
		{".ai", ".ai/"},
	}
	var missing []string
	for _, w := range want {
		if !entries[w.bare] {
			missing = append(missing, w.display)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf(".gitignore missing entries: %s", strings.Join(missing, ", "))
	}
	return ""
}

// parseGitignoreEntries returns the set of bare (trailing-slash-stripped)
// .gitignore patterns, skipping blank lines and comments.
func parseGitignoreEntries(content string) map[string]bool {
	entries := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries[strings.TrimRight(line, "/")] = true
	}
	return entries
}

// checkArtifactDirs verifies the .ai artifact directory is usable
// (either exists and is writable, or can be created).
func checkArtifactDirs(workdir string) CheckResult {
	out := CheckResult{Name: "Artifact Directories"}
	aiDir := filepath.Join(workdir, ".ai")
	info, err := os.Stat(aiDir)

	var detail CheckDetail
	allOk := true
	switch {
	case err == nil:
		detail, allOk = artifactExistingDirDetail(aiDir, info)
	case os.IsNotExist(err):
		detail, allOk = artifactMissingDirDetail(aiDir, workdir)
	default:
		// Non-ENOENT stat failure — permission denied, I/O error, etc.
		// Report the real failure instead of pretending .ai is missing.
		detail = CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("cannot inspect %s: %v", aiDir, err),
			Hint:    fmt.Sprintf("check permissions on %s and its parents", aiDir),
		}
		allOk = false
	}
	out.Details = append(out.Details, detail)

	if allOk {
		out.Status = CheckStatusOK
		out.Message = "artifact directories writable"
		return out
	}
	finalizeArtifactErrorStatus(&out)
	return out
}

// artifactExistingDirDetail classifies an existing .ai path (must be a
// writable directory), returning the detail line and whether it's OK.
func artifactExistingDirDetail(aiDir string, info os.FileInfo) (CheckDetail, bool) {
	switch {
	case !info.IsDir():
		return CheckDetail{
			Status:  CheckStatusError,
			Message: ".ai is not a directory",
		}, false
	case !isDirWritable(aiDir):
		return CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("%s exists but is not writable", aiDir),
			Hint:    fmt.Sprintf("check permissions: chmod u+w %s", aiDir),
		}, false
	default:
		return CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("%s exists and is writable", aiDir),
		}, true
	}
}

// artifactMissingDirDetail handles a not-yet-existing .ai path: OK when the
// parent is writable (created on first run), error otherwise.
func artifactMissingDirDetail(aiDir, workdir string) (CheckDetail, bool) {
	if isDirWritable(workdir) {
		return CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("%s will be created on first run", aiDir),
		}, true
	}
	return CheckDetail{
		Status:  CheckStatusError,
		Message: fmt.Sprintf("%s cannot be created (parent not writable)", aiDir),
	}, false
}

// finalizeArtifactErrorStatus sets the summary for a not-all-OK result,
// promoting to error when any detail is an error rather than a warning.
func finalizeArtifactErrorStatus(out *CheckResult) {
	out.Status = CheckStatusWarn
	for _, d := range out.Details {
		if d.Status == CheckStatusError {
			out.Status = CheckStatusError
			break
		}
	}
	out.Message = "some artifact directories have permission issues"
	out.Hint = "fix directory permissions: chmod u+w .ai"
}

func isDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".tracker_probe_*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// checkDiskSpace warns when available disk space under workdir is low.
// The implementation is platform-specific; see tracker_doctor_unix.go and
// tracker_doctor_windows.go.
