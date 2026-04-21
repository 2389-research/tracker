// ABOUTME: Repository localization pre-processing — identifies files relevant to a task
// ABOUTME: prompt via pure text analysis + filesystem scan (no LLM calls).
package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Localization tuning constants. Kept unexported to avoid leaking knobs into the public API.
const (
	localizeMaxFiles        = 10
	localizeSnippetLines    = 5
	localizeMaxInjectBytes  = 2048
	localizeMaxFilesToScan  = 2000
	localizeMaxFileSize     = 256 * 1024 // skip very large files to keep the scan fast
	localizeMaxPromptTokens = 200        // cap identifier extraction to keep scan bounded
)

// extractedRefs holds references extracted from a user prompt.
type extractedRefs struct {
	// Paths are file paths or basenames explicitly mentioned (e.g. "auth.go", "src/main.go").
	Paths []string
	// Identifiers are symbol-like tokens (camelCase / snake_case / PascalCase) from the prompt.
	Identifiers []string
	// Phrases are quoted or error-like substrings searched verbatim in file contents.
	Phrases []string
}

// File path / basename with extension. Kept conservative to avoid matching version numbers.
var pathOrFileRE = regexp.MustCompile(`(?:[A-Za-z0-9_./\-]+/)?[A-Za-z0-9_\-]+\.[A-Za-z0-9]{1,8}`)

// camelCase / PascalCase identifiers of length >= 4 (e.g. fooBar, FooBar, HTTPServer).
var camelCaseRE = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9]*[a-z][A-Z][a-zA-Z0-9]*\b`)

// snake_case identifiers with at least one underscore (e.g. foo_bar, handle_request).
var snakeCaseRE = regexp.MustCompile(`\b[a-z][a-z0-9]+(?:_[a-z0-9]+)+\b`)

// Quoted phrases ("...", '...', `...`). Captured group 1 is the phrase text.
var quotedPhraseRE = regexp.MustCompile("\"([^\"\\n]{3,80})\"|'([^'\\n]{3,80})'|`([^`\\n]{3,80})`")

// Error-style lines: "Error: ...", "error: ...", "FAIL: ...", "panic: ...".
var errorLineRE = regexp.MustCompile(`(?im)(?:^|\b)(?:error|fail|panic|fatal)\s*[:\-]\s*([^\n]{3,120})`)

// Common words that match the identifier heuristics but add no localization signal.
var identifierStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "this": true,
	"that": true, "have": true, "will": true, "should": true, "would": true, "could": true,
	"there": true, "when": true, "where": true, "which": true, "while": true, "your": true,
	"into": true, "about": true, "these": true, "those": true, "them": true, "they": true,
}

// Directories skipped during filesystem scan (dependency caches / VCS / build artifacts).
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"dist": true, "build": true, ".venv": true, "venv": true, "__pycache__": true,
	".idea": true, ".vscode": true, ".next": true, ".cache": true,
}

// extractRefs pulls candidate file paths, identifiers, and phrases from a prompt.
func extractRefs(prompt string) extractedRefs {
	if len(prompt) == 0 {
		return extractedRefs{}
	}

	refs := extractedRefs{}
	seen := map[string]bool{}

	addUnique := func(dst *[]string, v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		*dst = append(*dst, v)
	}

	for _, m := range pathOrFileRE.FindAllString(prompt, -1) {
		// Filter out URL-like or version-like false positives (e.g. "1.0.0", "v2.1").
		if strings.HasPrefix(m, "http://") || strings.HasPrefix(m, "https://") {
			continue
		}
		if isLikelyVersion(m) {
			continue
		}
		addUnique(&refs.Paths, m)
	}

	identSeen := map[string]bool{}
	addIdent := func(v string) {
		lower := strings.ToLower(v)
		if identifierStopwords[lower] || identSeen[v] {
			return
		}
		if len(identSeen) >= localizeMaxPromptTokens {
			return
		}
		identSeen[v] = true
		refs.Identifiers = append(refs.Identifiers, v)
	}
	for _, m := range camelCaseRE.FindAllString(prompt, -1) {
		addIdent(m)
	}
	for _, m := range snakeCaseRE.FindAllString(prompt, -1) {
		addIdent(m)
	}

	for _, m := range quotedPhraseRE.FindAllStringSubmatch(prompt, -1) {
		phrase := firstNonEmpty(m[1], m[2], m[3])
		if phrase == "" {
			continue
		}
		// Skip phrases that are already captured as paths.
		if seen[phrase] {
			continue
		}
		refs.Phrases = append(refs.Phrases, phrase)
	}
	for _, m := range errorLineRE.FindAllStringSubmatch(prompt, -1) {
		if len(m) >= 2 {
			refs.Phrases = append(refs.Phrases, strings.TrimSpace(m[1]))
		}
	}

	return refs
}

// isLikelyVersion returns true for strings that look like version numbers (e.g. "1.0.0", "v2.1.3").
func isLikelyVersion(s string) bool {
	trimmed := strings.TrimPrefix(s, "v")
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// candidate is a file identified as relevant, with a score and snippet.
type candidate struct {
	Path    string
	Score   int
	Snippet string
}

// localizeResult holds the outcome of the localization pass.
type localizeResult struct {
	Candidates []candidate
	Message    string // formatted context block suitable for injection, empty if no matches
}

// localize scans workingDir for files relevant to the prompt and builds a
// context block capped at localizeMaxFiles / localizeMaxInjectBytes. It never
// makes an LLM call and returns an empty result when no references match.
func localize(workingDir, prompt string) localizeResult {
	refs := extractRefs(prompt)
	if len(refs.Paths) == 0 && len(refs.Identifiers) == 0 && len(refs.Phrases) == 0 {
		return localizeResult{}
	}

	root := workingDir
	if root == "" {
		root = "."
	}

	files := scanFiles(root)
	scored := scoreFiles(root, files, refs)
	if len(scored) == 0 {
		return localizeResult{}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Path < scored[j].Path
	})

	if len(scored) > localizeMaxFiles {
		scored = scored[:localizeMaxFiles]
	}

	// Generate snippets and build the injection block, respecting the byte cap.
	msg := buildInjection(root, scored, refs)
	return localizeResult{Candidates: scored, Message: msg}
}

// scanFiles walks root and returns candidate file paths (relative to root),
// capped at localizeMaxFilesToScan. Skips binary-looking files, large files,
// and known dependency/vcs directories.
func scanFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= localizeMaxFilesToScan {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if !looksLikeTextFile(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > localizeMaxFileSize {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
}

// looksLikeTextFile uses extension heuristics to avoid reading binaries.
// Files without extensions are accepted (common for scripts / configs).
func looksLikeTextFile(rel string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	if ext == "" {
		return true
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".pdf", ".zip", ".tar", ".gz", ".bz2", ".7z", ".rar",
		".exe", ".dll", ".so", ".dylib", ".a", ".o", ".bin",
		".mp3", ".mp4", ".wav", ".ogg", ".mov", ".avi",
		".class", ".jar", ".wasm", ".pyc":
		return false
	}
	return true
}

// scoreFiles assigns relevance scores to each candidate file based on refs.
// Path mentions are heavily weighted; content matches are weighted lower.
func scoreFiles(root string, files []string, refs extractedRefs) []candidate {
	// Normalize path refs: for each, keep the basename and the full form.
	pathTerms := make([]string, 0, len(refs.Paths)*2)
	for _, p := range refs.Paths {
		pathTerms = append(pathTerms, p)
		if base := filepath.Base(p); base != p {
			pathTerms = append(pathTerms, base)
		}
	}

	var results []candidate
	for _, rel := range files {
		score := 0
		baseName := filepath.Base(rel)
		for _, t := range pathTerms {
			if baseName == t || rel == t {
				score += 50
			} else if strings.Contains(rel, t) {
				score += 20
			}
		}
		// Only read file contents when we have something to search for.
		if len(refs.Identifiers) > 0 || len(refs.Phrases) > 0 {
			contentScore := scoreFileContent(filepath.Join(root, rel), refs)
			score += contentScore
		}
		if score > 0 {
			results = append(results, candidate{Path: rel, Score: score})
		}
	}
	return results
}

// scoreFileContent reads the file and returns a score based on identifier/phrase matches.
// Each unique identifier match contributes up to 3 points (diminishing returns beyond 1 hit).
// Each phrase match contributes 5 points.
func scoreFileContent(abs string, refs extractedRefs) int {
	data, err := os.ReadFile(abs)
	if err != nil {
		return 0
	}
	content := string(data)
	score := 0
	for _, id := range refs.Identifiers {
		if strings.Contains(content, id) {
			score += 3
		}
	}
	for _, phrase := range refs.Phrases {
		if strings.Contains(content, phrase) {
			score += 5
		}
	}
	return score
}

// buildInjection formats the localization block, capped at localizeMaxInjectBytes.
// Snippets are the first matching window (localizeSnippetLines lines) containing
// any identifier or phrase, or the first N lines if no match location is found.
func buildInjection(root string, cands []candidate, refs extractedRefs) string {
	var b strings.Builder
	b.WriteString("Relevant files identified for this task (localization pre-processing, no LLM calls):\n")
	base := b.Len()

	for i := range cands {
		snippet := extractSnippet(filepath.Join(root, cands[i].Path), refs)
		cands[i].Snippet = snippet

		entry := fmt.Sprintf("\n- %s\n", cands[i].Path)
		if snippet != "" {
			entry += "```\n" + snippet + "\n```\n"
		}
		// Honor the byte cap: stop adding entries once we would exceed it.
		if b.Len()+len(entry) > localizeMaxInjectBytes {
			break
		}
		b.WriteString(entry)
	}

	if b.Len() == base {
		// Nothing fit within the cap (e.g. header alone would exceed).
		return ""
	}
	return b.String()
}

// extractSnippet returns up to localizeSnippetLines lines from the file,
// centered on the first line matching any identifier or phrase. Falls back to
// the file's leading lines if no match is located.
func extractSnippet(abs string, refs extractedRefs) string {
	f, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) >= 2000 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}

	matchLine := -1
	for i, line := range lines {
		for _, id := range refs.Identifiers {
			if strings.Contains(line, id) {
				matchLine = i
				break
			}
		}
		if matchLine >= 0 {
			break
		}
		for _, p := range refs.Phrases {
			if strings.Contains(line, p) {
				matchLine = i
				break
			}
		}
		if matchLine >= 0 {
			break
		}
	}

	start := 0
	if matchLine >= 0 {
		start = matchLine - localizeSnippetLines/2
		if start < 0 {
			start = 0
		}
	}
	end := start + localizeSnippetLines
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}
