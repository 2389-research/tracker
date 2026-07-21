// ABOUTME: Starter-file scaffolding for `tracker init` — so a newcomer's first
// ABOUTME: `tracker build_product` succeeds instead of hard-exiting on a missing SPEC.md.
package main

// workflowsNeedingSpec are the built-in workflows whose first node reads a
// SPEC.md from the repo root and hard-exits without one. `tracker init` on any
// of these also drops a starter SPEC.md (if absent) so the documented path —
// init → edit → run — works out of the box (issue #456).
var workflowsNeedingSpec = map[string]bool{
	"build_product":                true,
	"build_product_with_superspec": true,
}

const starterSpecFile = "SPEC.md"

// starterSpecMarkdown is a minimal but genuinely buildable spec. It's small on
// purpose — a cheap, fast first run that actually reaches a green result — and
// self-documents that the user should replace it with their own.
const starterSpecMarkdown = `# SPEC.md — starter project spec

> Created by ` + "`tracker init build_product`" + `. This is a *starter* spec so your
> first run succeeds end-to-end — **replace it with what you actually want built**,
> then run ` + "`tracker build_product`" + `. Keep your first spec small; you can grow it.

## Goal

Build a small Go command-line tool named ` + "`greet`" + ` that prints a friendly greeting.

## Requirements

1. ` + "`greet <name>`" + ` prints ` + "`Hello, <name>!`" + `.
2. ` + "`greet`" + ` with no argument prints ` + "`Hello, world!`" + `.
3. ` + "`greet --shout <name>`" + ` prints the greeting uppercased (e.g. ` + "`HELLO, ADA!`" + `).

## Verification

- ` + "`go build ./...`" + ` succeeds.
- ` + "`greet Ada`" + ` prints exactly ` + "`Hello, Ada!`" + `.
- ` + "`greet`" + ` prints exactly ` + "`Hello, world!`" + `.
- ` + "`greet --shout Ada`" + ` prints exactly ` + "`HELLO, ADA!`" + `.

## Constraints

- A single Go module, standard library only.
- Include a short ` + "`README.md`" + ` with usage examples.
`

// scaffoldStarterSpec writes a starter SPEC.md for a workflow that needs one,
// unless the file already exists (never overwrite the user's spec). Returns the
// filename written ("" if nothing was written) and any write error.
func scaffoldStarterSpec(workflowName string, exists func(string) bool, write func(string, []byte) error) (string, error) {
	if !workflowsNeedingSpec[workflowName] {
		return "", nil
	}
	if exists(starterSpecFile) {
		return "", nil // respect an existing spec
	}
	if err := write(starterSpecFile, []byte(starterSpecMarkdown)); err != nil {
		return "", err
	}
	return starterSpecFile, nil
}
