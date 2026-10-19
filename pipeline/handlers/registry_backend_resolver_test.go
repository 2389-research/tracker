// ABOUTME: The per-run BackendResolver reaches the codergen handler and is shared,
// ABOUTME: by pointer, with child registries built by NewRegistryFactory (subgraphs).
package handlers

import (
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func autoGraph() *pipeline.Graph {
	g := pipeline.NewGraph("auto")
	g.AddNode(&pipeline.Node{ID: "A", Shape: "box", Attrs: map[string]string{"backend": "auto"}})
	return g
}

func codergenFrom(t *testing.T, reg *pipeline.HandlerRegistry) *CodergenHandler {
	t.Helper()
	h, ok := reg.Get("codergen").(*CodergenHandler)
	if !ok {
		t.Fatalf("registry has no *CodergenHandler (got %T)", reg.Get("codergen"))
	}
	return h
}

func TestRegistry_WithBackendResolver(t *testing.T) {
	r := pipeline.NewBackendResolver()
	reg := NewDefaultRegistry(autoGraph(), WithBackendResolver(r))
	if codergenFrom(t, reg).backendResolver != r {
		t.Fatal("codergen handler does not hold the run's BackendResolver")
	}
}

func TestRegistryFactory_SharesBackendResolver(t *testing.T) {
	r := pipeline.NewBackendResolver()
	factory := NewRegistryFactory(WithBackendResolver(r))
	child := factory(autoGraph(), "Parent")
	if codergenFrom(t, child).backendResolver != r {
		t.Fatal("child registry's codergen handler has a different BackendResolver; subgraphs would probe again")
	}
}
