// ABOUTME: Tests for BackendResolver: backend: auto resolves at most once per run,
// ABOUTME: a failed probe is not cached, and a seeded (checkpoint) value is validated.
package pipeline

import (
	"errors"
	"sync"
	"testing"

	"github.com/2389-research/tracker/internal/testutil"
)

func TestBackendResolver_ResolveOnce(t *testing.T) {
	r := NewBackendResolver()
	w := testutil.NewWitness("probe", "cached-read")
	probe := func() (BackendResolution, error) {
		w.Hit("probe")
		return BackendResolution{Backend: "claude-code", Reason: "claude found"}, nil
	}
	const n = 16
	var wg sync.WaitGroup
	got := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := r.Resolve(probe)
			if err != nil {
				t.Errorf("Resolve: %v", err)
				return
			}
			w.Hit("cached-read")
			got[i] = res.Backend
		}(i)
	}
	wg.Wait()
	if w.Count("probe") != 1 || w.Count("cached-read") != n {
		t.Fatalf("want probe=1 cached-read=%d, got %s", n, w)
	}
	for i, b := range got {
		if b != "claude-code" {
			t.Fatalf("goroutine %d saw backend %q, want claude-code (%s)", i, b, w)
		}
	}
}

func TestBackendResolver_ProbeErrorNotCached(t *testing.T) {
	r := NewBackendResolver()
	calls := 0
	probe := func() (BackendResolution, error) {
		calls++
		if calls == 1 {
			return BackendResolution{}, errors.New("no backend available")
		}
		return BackendResolution{Backend: "native"}, nil
	}
	if _, err := r.Resolve(probe); err == nil {
		t.Fatal("first Resolve: want error")
	}
	res, err := r.Resolve(probe)
	if err != nil || res.Backend != "native" {
		t.Fatalf("second Resolve = %+v, %v; want native, nil", res, err)
	}
	if calls != 2 {
		t.Fatalf("probe calls = %d, want 2 (an error must not be cached)", calls)
	}
}

func TestBackendResolver_SeedValidates(t *testing.T) {
	for _, bad := range []string{"acp", "auto", "junk", ""} {
		if err := NewBackendResolver().Seed(bad); err == nil {
			t.Errorf("Seed(%q): want error", bad)
		}
	}
	for _, good := range []string{"native", "claude-code"} {
		r := NewBackendResolver()
		if err := r.Seed(good); err != nil {
			t.Fatalf("Seed(%q): %v", good, err)
		}
		probed := false
		res, err := r.Resolve(func() (BackendResolution, error) {
			probed = true
			return BackendResolution{Backend: "native"}, nil
		})
		if err != nil || res.Backend != good || !res.FromRecord || probed {
			t.Fatalf("seeded %q: Resolve = %+v, %v, probed=%v; want %q FromRecord, no probe", good, res, err, probed, good)
		}
	}
}

func TestBackendResolver_ResultBeforeAndAfter(t *testing.T) {
	r := NewBackendResolver()
	if _, ok := r.Result(); ok {
		t.Fatal("Result before resolution: want ok=false")
	}
	_, _ = r.Resolve(func() (BackendResolution, error) { return BackendResolution{Backend: "native"}, nil })
	if res, ok := r.Result(); !ok || res.Backend != "native" {
		t.Fatalf("Result after resolution = %+v, %v; want native, true", res, ok)
	}
}

func TestWithBackendResolver_SetsEngineField(t *testing.T) {
	r := NewBackendResolver()
	g := NewGraph("t")
	e := NewEngine(g, NewHandlerRegistry(), WithBackendResolver(r))
	if e.backendResolver != r {
		t.Fatal("WithBackendResolver did not set the engine's resolver")
	}
}
