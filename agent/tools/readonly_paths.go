// ABOUTME: tool_access: read_only path guard: read / grep_search / glob refuse paths that
// ABOUTME: resolve outside the workdir and secret-looking files (their contents go to an LLM).
package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/2389-research/tracker/agent/exec"
)

// NewReadOnlyTools returns exactly the read_only tool set — read, grep_search
// and glob — each guarded by CheckReadOnlyPath against env's working dir.
func NewReadOnlyTools(env exec.ExecutionEnvironment) []Tool {
	guard := func(p string) error { return CheckReadOnlyPath(env.WorkingDir(), p) }
	read := NewReadTool(env)
	read.guard = guard
	grep := NewGrepSearchTool(env)
	grep.guard = guard
	glob := NewGlobTool(env)
	glob.guard = guard
	return []Tool{read, grep, glob}
}

// CheckReadOnlyPath refuses p (relative to root, or absolute) when it
// resolves — symlinks included — outside root, or names a secret-looking file:
// .env and .env.* (except .env.example / .env.sample), id_rsa*, *.p12, and
// *.pem / *.key files that contain a private key.
func CheckReadOnlyPath(root, p string) error {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	resolved, err := evalSymlinksLeniently(abs)
	if err != nil {
		return fmt.Errorf("read_only: cannot resolve %s: %w", p, err)
	}
	resolvedRoot, err := evalSymlinksLeniently(root)
	if err != nil {
		return fmt.Errorf("read_only: cannot resolve the working directory: %w", err)
	}
	if !withinDir(resolvedRoot, resolved) {
		return fmt.Errorf("read_only: %s resolves outside the working directory", p)
	}
	if isSecretFile(resolved) {
		return fmt.Errorf("read_only: %s looks like a secret and is not readable in a read_only session", p)
	}
	return nil
}

func withinDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var privateKeyBlock = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)

func isSecretFile(path string) bool {
	name := filepath.Base(path)
	if isSecretName(name) {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	return (ext == ".pem" || ext == ".key") && containsPrivateKey(path)
}

func isSecretName(name string) bool {
	switch {
	case name == ".env", strings.HasPrefix(name, "id_rsa"), strings.HasSuffix(strings.ToLower(name), ".p12"):
		return true
	case strings.HasPrefix(name, ".env."):
		return name != ".env.example" && name != ".env.sample"
	}
	return false
}

func containsPrivateKey(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	return privateKeyBlock.Match(head)
}
