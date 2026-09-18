// ABOUTME: os.Root-backed in-process tier for writable_paths_mode: prefer on a host
// ABOUTME: without Landlock or openat2 (#648) — glob policy + per-component no-symlink walk.
package handlers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	execpkg "github.com/2389-research/tracker/agent/exec"
)

// installRootInProcess is the portable degraded in-process tier: the glob
// policy, then os.Root-scoped MkdirAll / OpenFile / Remove so every path
// component resolves beneath the anchor (an escape is refused by the
// kernel-level per-component walk os.Root performs, not by a lexical check).
// The Root is opened per call so a replaced anchor directory is never served
// from a stale descriptor. Order matters: MkdirAll runs AFTER the glob check
// so a rejected write leaves no empty directories.
func installRootInProcess(env *execpkg.LocalEnvironment, anchor string, globs []string) {
	env.WriteOpener = func(absPath string, perm os.FileMode) (*os.File, error) {
		relPath, err := jailPolicyCheck(anchor, absPath, globs)
		if err != nil {
			return nil, err
		}
		return rootOpenForWrite(anchor, relPath, perm)
	}
	env.Remover = func(absPath string) error {
		relPath, err := jailPolicyCheck(anchor, absPath, globs)
		if err != nil {
			return err
		}
		return rootRemove(anchor, relPath)
	}
}

// rootOpenForWrite creates relPath's parents and opens it for writing, every
// component resolved beneath anchor by os.Root. Before any mutation,
// rootRefuseSymlinks Lstat's every component of relPath (prefixes AND the
// leaf) and refuses a symlink anywhere — mirroring openat2's
// RESOLVE_NO_SYMLINKS, which the enforced tier gets from the kernel. os.Root
// alone only refuses links whose target leaves the root; a RELATIVE in-anchor
// link (`ok -> .`, `ok/leaf -> ../secret/t`) resolves inside the root and
// would let a glob-approved path land a write under a directory the glob did
// not name (review round 2). Residual: TOCTOU between the Lstat walk and the
// open — a same-UID process racing a link into place after the check is not
// defended (the openat2 tier has no such window); out-of-anchor targets stay
// kernel-refused regardless.
func rootOpenForWrite(anchor, relPath string, perm os.FileMode) (*os.File, error) {
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, fmt.Errorf("open anchor %q: %w", anchor, err)
	}
	defer root.Close()
	if err := rootRefuseSymlinks(root, relPath); err != nil {
		return nil, rootEscapeErr(anchor, relPath, err)
	}
	if err := root.MkdirAll(filepath.Dir(relPath), 0o755); err != nil {
		return nil, rootEscapeErr(anchor, relPath, err)
	}
	f, err := root.OpenFile(relPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return nil, rootEscapeErr(anchor, relPath, err)
	}
	return f, nil
}

// rootRemove unlinks relPath with every component resolved beneath anchor,
// after the same no-symlink walk over the PREFIX components as
// rootOpenForWrite (the leaf itself may be a symlink: Remove unlinks the link,
// never its target).
func rootRemove(anchor, relPath string) error {
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return fmt.Errorf("open anchor %q: %w", anchor, err)
	}
	defer root.Close()
	if err := rootRefuseSymlinks(root, filepath.Dir(relPath)); err != nil {
		return rootEscapeErr(anchor, relPath, err)
	}
	if err := root.Remove(relPath); err != nil {
		return rootEscapeErr(anchor, relPath, err)
	}
	return nil
}

// rootRefuseSymlinks Lstat's every component of relPath from the root down
// and returns errRootEscapes at the first symlink. A missing component ends
// the walk (nothing below it can exist yet); any other Lstat error is
// returned as-is.
func rootRefuseSymlinks(root *os.Root, relPath string) error {
	relPath = filepath.Clean(relPath)
	if relPath == "." {
		return nil
	}
	for _, prefix := range pathPrefixes(relPath) {
		fi, err := root.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q is a symlink", errRootEscapes, prefix)
		}
	}
	return nil
}

// pathPrefixes returns every leading sub-path of a cleaned relative path,
// shortest first: "a/b/c" -> ["a", "a/b", "a/b/c"].
func pathPrefixes(relPath string) []string {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	out := make([]string, 0, len(parts))
	for i := range parts {
		out = append(out, filepath.FromSlash(strings.Join(parts[:i+1], "/")))
	}
	return out
}

// errRootEscapes mirrors os.Root's unexported "path escapes from parent"
// sentinel text so rootEscapeErr classifies both the kernel-level refusal and
// the symlinked-intermediate case the same way.
var errRootEscapes = errors.New("path escapes from parent")

// rootEscapeErr classifies an os.Root failure: a path that escaped the root
// (os reports "path escapes from parent" on the PathError) is surfaced as
// ErrPathEscape so callers and tests see the same sentinel the openat2 tier
// uses; anything else is wrapped as-is.
func rootEscapeErr(anchor, relPath string, err error) error {
	if strings.Contains(err.Error(), "escapes from parent") {
		return fmt.Errorf("%w: %q under %q: %v", execpkg.ErrPathEscape, relPath, anchor, err)
	}
	return fmt.Errorf("%q under %q: %w", relPath, anchor, err)
}
