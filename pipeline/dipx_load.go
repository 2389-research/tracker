// ABOUTME: Loads .dipx bundles produced by dippin v0.24+ — verifies hashes,
// ABOUTME: converts pre-parsed IR to tracker Graphs, returns content-addressed identity.
package pipeline

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/2389-research/dippin-lang/dipx"
	"github.com/2389-research/dippin-lang/validator"
)

// BundleInfo carries the metadata extracted from a loaded .dipx bundle.
// Identity is the canonical "sha256:<64 hex>" form of the bundle's
// content-addressed hash (SHA-256 of manifest.json bytes-as-stored).
// EntryPath is the canonical bundle-relative path of the entry workflow.
type BundleInfo struct {
	Identity  string
	EntryPath string
	Manifest  dipx.Manifest
}

// LoadDipxBundle opens a .dipx file, verifies all SHA-256 hashes via
// dipx.Open (strict mode), converts the entry workflow and every transitively-
// referenced subgraph from pre-parsed IR to tracker Graphs, and returns the
// graphs plus a BundleInfo carrying the bundle's content-addressed identity.
//
// The subgraphs map is keyed by canonical bundle path (matching manifest.Files
// entries). dipx has already verified ref closure and acyclicity, so no
// recursive walk is needed on tracker's side.
//
// After IR-to-Graph conversion, every subgraph_ref attr on every loaded graph
// is rewritten from the author's source ref (e.g., "sub.dip") to the canonical
// bundle path (e.g., "workflows/sub.dip") so refs match the subgraphs map keys.
//
// Diagnostics from the bundled IR (lint warnings; DIP126 "subgraph ref does
// not exist" is filtered since dipx already verified ref closure) are
// returned for the caller to log. The library deliberately does not write to
// os.Stderr here so embedded callers can route output through their own
// logger; the CLI wrapper prints them to stderr, mirroring the .dip path.
func LoadDipxBundle(ctx context.Context, path string) (*Graph, map[string]*Graph, BundleInfo, []validator.Diagnostic, error) {
	bundle, err := dipx.Open(ctx, path)
	if err != nil {
		return nil, nil, BundleInfo{}, nil, fmt.Errorf("load bundle %s: %w", path, err)
	}
	manifest := bundle.Manifest()

	var diagnostics []validator.Diagnostic

	entry := bundle.Entry()
	entryGraph, diags, err := LoadDippinWorkflowFromIR(entry, manifest.Entry)
	diagnostics = append(diagnostics, filterBundleLintNoise(diags)...)
	if err != nil {
		return nil, nil, BundleInfo{}, diagnostics, fmt.Errorf("load bundle %s: entry %s: %w", path, manifest.Entry, err)
	}
	// Same DIP126 suppression applies to LintWarnings the graph carries
	// into ValidateAll/-WithLint, so the "Validation Warnings" output
	// matches the stderr log emitted at load time.
	entryGraph.LintWarnings = filterBundleLintWarnings(entryGraph.LintWarnings)
	if err := canonicalizeSubgraphRefs(entryGraph, bundle, manifest.Entry); err != nil {
		return nil, nil, BundleInfo{}, diagnostics, fmt.Errorf("load bundle %s: entry %s: %w", path, manifest.Entry, err)
	}

	subgraphs := make(map[string]*Graph)
	for _, file := range manifest.Files {
		if file.Path == manifest.Entry {
			continue
		}
		wf, err := bundle.Lookup(file.Path)
		if err != nil {
			return nil, nil, BundleInfo{}, diagnostics, fmt.Errorf("load bundle %s: lookup %s: %w", path, file.Path, err)
		}
		sub, subDiags, err := LoadDippinWorkflowFromIR(wf, file.Path)
		diagnostics = append(diagnostics, filterBundleLintNoise(subDiags)...)
		if err != nil {
			return nil, nil, BundleInfo{}, diagnostics, fmt.Errorf("load bundle %s: subgraph %s: %w", path, file.Path, err)
		}
		sub.LintWarnings = filterBundleLintWarnings(sub.LintWarnings)
		if err := canonicalizeSubgraphRefs(sub, bundle, file.Path); err != nil {
			return nil, nil, BundleInfo{}, diagnostics, fmt.Errorf("load bundle %s: subgraph %s: %w", path, file.Path, err)
		}
		subgraphs[file.Path] = sub
	}

	id := bundle.Identity()
	info := BundleInfo{
		Identity:  "sha256:" + hex.EncodeToString(id[:]),
		EntryPath: manifest.Entry,
		Manifest:  manifest,
	}
	return entryGraph, subgraphs, info, diagnostics, nil
}

// filterBundleLintNoise drops diagnostics that don't apply when the source is
// a sealed .dipx bundle. Currently strips DIP126 ("subgraph ref file does not
// exist") — dippin's lint does an os.Stat on the bundle-relative path (e.g.,
// "workflows/sub.dip"), but bundled subgraphs live inside the ZIP and dipx.Open
// has already verified ref closure for us. Without this filter every load of a
// bundle with subgraphs would print a misleading "does not exist" warning.
func filterBundleLintNoise(diags []validator.Diagnostic) []validator.Diagnostic {
	out := make([]validator.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Code == validator.DIP126 {
			continue
		}
		out = append(out, d)
	}
	return out
}

// filterBundleLintWarnings mirrors filterBundleLintNoise for the pre-formatted
// Graph.LintWarnings slice. The single-line "warning[DIP126]: ..." form is
// matched on its code prefix so the suppression stays in lockstep with the
// validator.Diagnostic filter above.
func filterBundleLintWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return warnings
	}
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		if strings.HasPrefix(w, "warning["+validator.DIP126+"]") {
			continue
		}
		out = append(out, w)
	}
	return out
}

// canonicalizeSubgraphRefs rewrites every subgraph_ref attr on g from the
// author's source ref to the canonical bundle path returned by bundle.Resolve.
// parentBundlePath is the canonical bundle path of g itself, used as the
// "relative to" anchor for ref resolution. After this, subgraph_ref values
// match the keys in the subgraphs map returned by LoadDipxBundle, so callers
// like validateSubgraphRefs and the subgraph handler can do direct lookups.
func canonicalizeSubgraphRefs(g *Graph, bundle *dipx.Bundle, parentBundlePath string) error {
	for _, node := range g.Nodes {
		// subgraph_ref has no typed accessor on *pipeline.Node (yet); this
		// matches the convention used elsewhere — pipeline/subgraph.go,
		// cmd/tracker/loading.go:loadSubgraphsRecursive, cmd/tracker/nodes.go,
		// and the manager_loop handler all read it directly via Attrs.
		ref := node.Attrs["subgraph_ref"]
		if ref == "" {
			continue
		}
		canonical, err := bundle.Resolve(ref, parentBundlePath)
		if err != nil {
			return fmt.Errorf("resolve subgraph ref %q from %s: %w", ref, parentBundlePath, err)
		}
		node.Attrs["subgraph_ref"] = canonical
	}
	return nil
}
