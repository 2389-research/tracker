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
			wantErr:    "malformed",
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
