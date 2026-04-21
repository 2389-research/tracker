// ABOUTME: Repository localization pre-processing — identifies files relevant to a task
// ABOUTME: prompt via pure text analysis + filesystem scan (no LLM calls).
package agent

import (
	"bufio"
	"context"
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
	localizeMaxPhrases      = 32         // cap on extracted phrases (quoted + error-line)
	localizeMaxContentScans = 200        // cap on files read for content scoring per localize call

	// scannerInitialBuf is the starting size of the line scanner buffer — large
	// enough to hold any reasonable source line without growing on typical input.
	scannerInitialBuf = 64 * 1024
	// scannerMaxLineBytes is the hard upper bound per line; if a line exceeds
	// this limit, bufio.Scanner stops and scanner.Err() becomes bufio.ErrTooLong.
	// Snippet extraction tolerates this by returning whatever lines were read
	// successfully (the first-N-lines fallback still produces a useful signal).
	scannerMaxLineBytes = 1024 * 1024
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

// File path / basename with extension. Accepts both Unix (/) and Windows (\)
// separators so prompts mentioning `src\main.go` are also recognized.
// Kept conservative to avoid matching version numbers.
var pathOrFileRE = regexp.MustCompile(`(?:[A-Za-z0-9_./\\\-]+[/\\])?[A-Za-z0-9_\-]+\.[A-Za-z0-9]{1,8}`)

// camelCase / PascalCase identifiers (e.g. fooBar, FooBar) and acronym-prefixed
// names (e.g. HTTPServer, URLParser). Two alternations:
//  1. lowercase→uppercase boundary (fooBar, FooBar)
//  2. 2+ consecutive uppercase letters followed by a lowercase (HTTPServer)
var camelCaseRE = regexp.MustCompile(`\b(?:[a-zA-Z][a-zA-Z0-9]*[a-z][A-Z][a-zA-Z0-9]*|[A-Z]{2,}[a-z][a-zA-Z0-9]*)\b`)

// snake_case identifiers with at least one underscore (e.g. foo_bar, handle_request).
var snakeCaseRE = regexp.MustCompile(`\b[a-z][a-z0-9]+(?:_[a-z0-9]+)+\b`)

// Quoted phrases ("...", '...', `...`). The phrase text is captured in
// group 1, 2, or 3 depending on which quote style matched.
var quotedPhraseRE = regexp.MustCompile("\"([^\"\\n]{3,80})\"|'([^'\\n]{3,80})'|`([^`\\n]{3,80})`")

// Error-style lines: "Error: ...", "error: ...", "FAIL: ...", "panic: ...".
// Flags: i = case-insensitive, m = multi-line (^ matches after a newline).
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
		// Normalize Windows-style separators so downstream matching is platform-agnostic.
		addUnique(&refs.Paths, strings.ReplaceAll(m, `\`, "/"))
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

	phraseSeen := map[string]bool{}
	addPhrase := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || phraseSeen[p] || seen[p] {
			return
		}
		if len(refs.Phrases) >= localizeMaxPhrases {
			return
		}
		phraseSeen[p] = true
		refs.Phrases = append(refs.Phrases, p)
	}
	for _, m := range quotedPhraseRE.FindAllStringSubmatch(prompt, -1) {
		addPhrase(firstNonEmpty(m[1], m[2], m[3]))
	}
	for _, m := range errorLineRE.FindAllStringSubmatch(prompt, -1) {
		if len(m) >= 2 {
			addPhrase(m[1])
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
// If ctx is canceled during the scan, an empty result is returned.
func localize(ctx context.Context, workingDir, prompt string) localizeResult {
	if ctx == nil {
		ctx = context.Background()
	}
	refs := extractRefs(prompt)
	if len(refs.Paths) == 0 && len(refs.Identifiers) == 0 && len(refs.Phrases) == 0 {
		return localizeResult{}
	}

	root := workingDir
	if root == "" {
		root = "."
	}

	files := scanFiles(ctx, root)
	if ctx.Err() != nil {
		return localizeResult{}
	}
	scored := scoreFiles(ctx, root, files, refs)
	if ctx.Err() != nil || len(scored) == 0 {
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
// and known dependency/vcs directories. Aborts early on context cancellation.
//
// When the localizeMaxFilesToScan cap is reached, the walk stops and results
// may be partial — relevant files that would have appeared later in traversal
// order are silently omitted. This is acceptable for a hint-only phase where
// predictable latency matters more than exhaustive coverage.
func scanFiles(ctx context.Context, root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
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

// safeDotfiles are common config/metadata dotfiles that are known to be
// non-sensitive and useful for localization context. All other dotfiles are
// skipped to reduce accidental leakage of credential files (.env, .netrc,
// .npmrc, .aws/credentials, etc.).
var safeDotfiles = map[string]bool{
	".editorconfig":          true,
	".gitignore":             true,
	".gitattributes":         true,
	".dockerignore":          true,
	".prettierrc":            true,
	".eslintrc":              true,
	".eslintignore":          true,
	".stylelintrc":           true,
	".clang-format":          true,
	".rubocop.yml":           true,
	".golangci.yml":          true,
	".golangci.yaml":         true,
	".pre-commit-hooks.yaml": true,
}

// looksLikeTextFile uses extension/name heuristics to avoid reading binaries
// and to reduce accidental inclusion of credentials. Hidden files (dotfiles
// such as .env, .npmrc) and common secret-bearing filenames are skipped so
// their contents are never injected into the LLM context. A narrow allowlist
// of well-known non-sensitive config dotfiles is permitted.
func looksLikeTextFile(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))

	// Skip dotfiles by default — reduces accidental leakage of credential
	// files. Allow a narrow set of well-known config dotfiles that are useful
	// for localization context.
	if strings.HasPrefix(base, ".") && !safeDotfiles[base] {
		return false
	}

	// Explicit block-list for extensionless secret files.
	switch base {
	case "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"authorized_keys", "known_hosts":
		return false
	}

	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	// Secret-bearing file extensions. Private-key formats (.pem, .key, .p12,
	// .pfx, .jks, .keystore) clearly contain secrets. Certificate formats
	// (.crt, .cer, .der) typically contain only public keys, but we block
	// them conservatively — a certificate file rarely carries useful source
	// context, and some tools bundle certificates with private keys.
	case ".pem", ".key", ".p12", ".pfx", ".jks", ".keystore",
		".crt", ".cer", ".der",
		// Binary/media/archive extensions.
		".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".pdf", ".zip", ".tar", ".gz", ".bz2", ".7z", ".rar",
		".exe", ".dll", ".so", ".dylib", ".a", ".o", ".bin",
		".mp3", ".mp4", ".wav", ".ogg", ".mov", ".avi",
		".class", ".jar", ".wasm", ".pyc":
		return false
	}
	// Extensionless non-dotfile (e.g. "Makefile", "LICENSE") is assumed text.
	return true
}

// scoreFiles assigns relevance scores to each candidate file based on refs.
// Path mentions are heavily weighted; content matches are weighted lower.
//
// To bound I/O on large repos, content is only scanned for files that either
// (a) already have a path-score signal (always worthwhile), or (b) fall within
// the first localizeMaxContentScans files encountered without a path-score
// signal. The fallback budget applies whether or not path refs were extracted,
// so related files that don't match any mentioned path can still be surfaced
// when the prompt also contains identifier or phrase references.
func scoreFiles(ctx context.Context, root string, files []string, refs extractedRefs) []candidate {
	// Normalize path refs: for each, keep the basename and the full form.
	pathTerms := make([]string, 0, len(refs.Paths)*2)
	for _, p := range refs.Paths {
		pathTerms = append(pathTerms, p)
		if base := filepath.Base(p); base != p {
			pathTerms = append(pathTerms, base)
		}
	}

	needsContent := len(refs.Identifiers) > 0 || len(refs.Phrases) > 0

	var results []candidate
	fallbackScansDone := 0
	for _, rel := range files {
		if ctx.Err() != nil {
			return results
		}
		score := 0
		baseName := filepath.Base(rel)
		for _, t := range pathTerms {
			if baseName == t || rel == t {
				score += 50
			} else if strings.Contains(rel, t) {
				score += 20
			}
		}
		// Content scanning is expensive (reads the full file). Limit it to:
		//  - files already flagged by path match (always worthwhile), or
		//  - the first localizeMaxContentScans non-path-match files
		//    (bounded cost), regardless of whether path refs were extracted.
		hasPathMatch := score > 0
		fallbackScanAllowed := !hasPathMatch && fallbackScansDone < localizeMaxContentScans
		shouldScan := needsContent && (hasPathMatch || fallbackScanAllowed)
		if shouldScan {
			if !hasPathMatch {
				fallbackScansDone++
			}
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
// Each identifier contributes 3 points at most once per file if it appears anywhere in the content.
// Each phrase contributes 5 points at most once per file if it appears anywhere in the content.
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
// If a file's full entry (path + snippet) would exceed the remaining cap, the
// snippet is dropped so at least the path is preserved as a localization signal.
func buildInjection(root string, cands []candidate, refs extractedRefs) string {
	var b strings.Builder
	b.WriteString("Relevant files identified for this task (localization pre-processing, no LLM calls):\n")
	base := b.Len()

	for i := range cands {
		snippet := extractSnippet(filepath.Join(root, cands[i].Path), refs)
		cands[i].Snippet = snippet

		pathOnly := fmt.Sprintf("\n- %s\n", cands[i].Path)
		full := pathOnly
		if snippet != "" {
			full += "```\n" + snippet + "\n```\n"
		}
		// Honor the byte cap. If the full entry doesn't fit, fall back to the
		// path alone so at least some localization signal is preserved. If even
		// the path doesn't fit, stop adding entries.
		switch {
		case b.Len()+len(full) <= localizeMaxInjectBytes:
			b.WriteString(full)
		case b.Len()+len(pathOnly) <= localizeMaxInjectBytes:
			b.WriteString(pathOnly)
		default:
			// No more room for even a bare path — stop early.
			return finalizeInjection(&b, base)
		}
	}

	return finalizeInjection(&b, base)
}

// finalizeInjection returns the built string, or "" when nothing was appended
// past the header (e.g. all entries exceeded the byte cap).
func finalizeInjection(b *strings.Builder, base int) string {
	if b.Len() == base {
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
	scanner.Buffer(make([]byte, scannerInitialBuf), scannerMaxLineBytes)
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
