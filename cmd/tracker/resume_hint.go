// ABOUTME: Builds the argument tail of the resume command printed after a failed run.
// ABOUTME: Repeats the run's -w and --artifact-dir, absolute and shell-quoted (#696).
package main

import (
	"path/filepath"
	"regexp"
	"strings"
)

// resumeHintTarget is the argument tail of the printed resume command: the
// pipeline file, preceded by the run's -w (when given) and --artifact-dir
// (when given). -r looks the run up in that artifact dir, and the run's nodes
// execute in that workdir, so a hint without them resumes in the wrong place
// (#696). Dirs are made absolute so the hint works from any cwd, and every
// argument is shell-quoted.
func (o *runOptions) resumeHintTarget() string {
	args := o.dirFlags()
	pipelineArg := "<pipeline.dip>"
	if o.pipelineFile != "" {
		pipelineArg = shellQuote(o.pipelineFile)
	}
	return strings.Join(append(args, pipelineArg), " ")
}

// absOrSelf returns p made absolute, or p unchanged when that fails.
func absOrSelf(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// shellSafe matches arguments a POSIX shell takes literally.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// shellQuote single-quotes s for a POSIX shell unless it is shellSafe.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// dirFlags returns the -w / --artifact-dir flags a follow-up command must
// repeat to find this run (absolute, shell-quoted), when they were given.
func (o *runOptions) dirFlags() []string {
	var args []string
	if o.workdirExplicit && o.workdir != "" {
		args = append(args, "-w", shellQuote(absOrSelf(o.workdir)))
	}
	if o.artifactDir != "" {
		args = append(args, "--artifact-dir", shellQuote(absOrSelf(o.artifactDir)))
	}
	return args
}
