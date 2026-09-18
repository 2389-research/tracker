// ABOUTME: fs.FS-backed resolver for dippin *_file directives (command_file, prompt_file, ...).
// ABOUTME: Lets embedded built-in workflows load their sidecars from the binary instead of disk.
package pipeline

import (
	"fmt"
	"io/fs"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
)

// ResolveFileDirectivesFS loads the file contents referenced by every *_file
// directive in w — a tool node's command_file into Command, an agent node's
// prompt_file / system_prompt_file / prompt_include into Prompt /
// SystemPrompt, and the defaults-block prompt cascade (prompt_prefix_file,
// prompt_suffix_file, system_prompt_file) — reading each path from fsys
// relative to baseDir instead of from disk.
//
// This is a thin wrapper over dippin's parser.ResolveFileDirectivesFS
// (dippin-lang#304, v0.75.0), which shares one cascade traversal with the
// disk resolver so the #175 prefix/suffix cascade, the #72 shared
// system-prompt fallback, and the #248 body-less passthrough skip cannot
// drift between the two. The exported name and signature are tracker's
// stable seam (LoadDippinWorkflowFS, the embedded loaders in
// tracker_workflows.go, and the CLI's resolveDirectives all call it);
// dippin_resolve_fs_test.go pins the contract tracker relies on — parity with
// the disk resolver on build_product, `..` / absolute rejection with the node
// and directive named, and "file %q not found" for a missing sidecar.
//
// Path safety, as documented upstream: absolute paths and any `..` segment
// are rejected before joining; the joined name must satisfy fs.ValidPath; the
// 4 MiB per-file cap applies; directories are errors; on an fs.ReadLinkFS
// (os.DirFS) every component below baseDir is Lstat-checked and symlinks are
// rejected. An FS that cannot report symlinks (embed.FS, fstest.MapFS) is
// trusted to be self-contained.
func ResolveFileDirectivesFS(w *ir.Workflow, fsys fs.FS, baseDir string) error {
	if w == nil {
		return fmt.Errorf("resolve file directives: nil workflow")
	}
	if fsys == nil {
		return fmt.Errorf("resolve file directives: nil fs")
	}
	return parser.ResolveFileDirectivesFS(w, fsys, baseDir)
}
