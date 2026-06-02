package exec

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateWritablePaths(t *testing.T) {
	cwd := "/home/user/run"
	cases := []struct {
		name       string
		workingDir string
		globs      []string
		wantErr    string // substring; empty = expect nil error
	}{
		{
			name:       "happy path single glob",
			workingDir: "/home/user/run/work",
			globs:      []string{"workspace/**"},
			wantErr:    "",
		},
		{
			name:       "happy path multiple globs",
			workingDir: "/home/user/run",
			globs:      []string{"workspace/**", ".ai/sprints/**"},
			wantErr:    "",
		},
		{
			name:       "working_dir absolute outside cwd is rejected",
			workingDir: "/tmp/atk",
			globs:      []string{"workspace/**"},
			wantErr:    "working_dir",
		},
		{
			name:       "working_dir parent escape rejected",
			workingDir: "/home/user/run/../../etc",
			globs:      []string{"workspace/**"},
			wantErr:    "working_dir",
		},
		{
			name:       "empty globs is fail-closed",
			workingDir: "/home/user/run",
			globs:      []string{},
			wantErr:    "empty",
		},
		{
			name:       "nil globs is fail-closed",
			workingDir: "/home/user/run",
			globs:      nil,
			wantErr:    "empty",
		},
		{
			name:       "absolute glob entry is rejected",
			workingDir: "/home/user/run",
			globs:      []string{"/etc/**"},
			wantErr:    "escape",
		},
		{
			name:       "tilde glob entry is rejected",
			workingDir: "/home/user/run",
			globs:      []string{"~/secrets/**"},
			wantErr:    "escape",
		},
		{
			name:       "parent-escape glob entry is rejected",
			workingDir: "/home/user/run",
			globs:      []string{"../../etc/**"},
			wantErr:    "escape",
		},
		{
			name:       "malformed brace glob is rejected",
			workingDir: "/home/user/run",
			globs:      []string{"workspace/*.{md"},
			wantErr:    "brace expansion",
		},
		// --- Validation gaps closed during #275 review ---
		{
			name:       "balanced braces also rejected (matcher does not expand)",
			workingDir: "/home/user/run",
			globs:      []string{"workspace/*.{md,yaml}"},
			wantErr:    "brace expansion",
		},
		{
			name:       "inward .. segment rejected (path.Clean collapse escalation)",
			workingDir: "/home/user/run",
			globs:      []string{"workspace/../**"},
			wantErr:    "..",
		},
		{
			name:       "metachar in prefix before ** rejected (matcher vs Landlock mismatch)",
			workingDir: "/home/user/run",
			globs:      []string{"work*/**"},
			wantErr:    "metachars before",
		},
		{
			name:       "metachar in middle segment of prefix/**/suffix rejected",
			workingDir: "/home/user/run",
			globs:      []string{"work*/**/report.md"},
			wantErr:    "metachars before",
		},
		{
			name:       "doublestar glued to chars rejected (foo/**bar)",
			workingDir: "/home/user/run",
			globs:      []string{"foo/**bar"},
			wantErr:    "must be its own path segment",
		},
		{
			name:       "multiple ** segments rejected",
			workingDir: "/home/user/run",
			globs:      []string{"a/**/b/**/c"},
			wantErr:    "only one",
		},
		{
			name:       "malformed character class rejected",
			workingDir: "/home/user/run",
			globs:      []string{"workspace/foo["},
			wantErr:    "malformed",
		},
		{
			name:       "happy: prefix/**/suffix is supported",
			workingDir: "/home/user/run",
			globs:      []string{"workspace/**/report.md"},
			wantErr:    "",
		},
		{
			name:       "happy: **/suffix is supported",
			workingDir: "/home/user/run",
			globs:      []string{"**/report.md"},
			wantErr:    "",
		},
		{
			name:       "happy: bare ** is supported",
			workingDir: "/home/user/run",
			globs:      []string{"**"},
			wantErr:    "",
		},
		{
			name:       "Windows absolute path rejected",
			workingDir: "/home/user/run",
			globs:      []string{`C:\foo\**`},
			wantErr:    "Windows",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWritablePaths(tc.workingDir, tc.globs, cwd)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("ValidateWritablePaths = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateWritablePaths = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
				t.Errorf("ValidateWritablePaths = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateWritablePaths_WrapsForErrorsIs(t *testing.T) {
	// Sentinel-identity classification: codergen handler (Task 14) calls
	// errors.Is(err, ErrPathEscape) to distinguish path-escape failures
	// from empty/Landlock-unavailable failures, so this contract must hold.
	err := ValidateWritablePaths("/home/user/run", []string{"/etc/**"}, "/home/user/run")
	if err == nil {
		t.Fatal("expected error for absolute glob")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
	}
}
