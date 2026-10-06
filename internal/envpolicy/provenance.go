// ABOUTME: Records which .env file supplied (or tried to supply) each environment variable.
// ABOUTME: The loader writes it; doctor reads it to say "from shell" / "from <file>" / "ignored".
package envpolicy

import (
	"sort"
	"sync"
)

// Origin is one loader decision about a variable from a file.
type Origin struct {
	// Name is the variable name.
	Name string
	// File is the .env path the value came from.
	File string
	// Applied is true when the value was set into the process environment.
	Applied bool
	// Reason explains a skip (empty when Applied).
	Reason string
}

var (
	provMu      sync.Mutex
	provApplied = map[string]Origin{}
	provSkipped []Origin
)

// ResetProvenance clears the record. The loader calls it at the start of a
// load; tests call it between cases.
func ResetProvenance() {
	provMu.Lock()
	defer provMu.Unlock()
	provApplied = map[string]Origin{}
	provSkipped = nil
}

// RecordApplied notes that name was set from file. A later file that sets
// the same name replaces the record.
func RecordApplied(name, file string) {
	provMu.Lock()
	defer provMu.Unlock()
	provApplied[name] = Origin{Name: name, File: file, Applied: true}
}

// RecordSkipped notes that file tried to set name and the loader refused.
func RecordSkipped(name, file, reason string) {
	provMu.Lock()
	defer provMu.Unlock()
	provSkipped = append(provSkipped, Origin{Name: name, File: file, Reason: reason})
}

// AppliedFrom returns the file that supplied name's current value, or ""
// when the value came from the shell (or is unset).
func AppliedFrom(name string) string {
	provMu.Lock()
	defer provMu.Unlock()
	return provApplied[name].File
}

// Skipped returns every refused file assignment, sorted by name then file.
func Skipped() []Origin {
	provMu.Lock()
	defer provMu.Unlock()
	out := make([]Origin, len(provSkipped))
	copy(out, provSkipped)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].File < out[j].File
	})
	return out
}
