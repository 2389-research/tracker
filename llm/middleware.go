// ABOUTME: Middleware types for the unified LLM client's request/response pipeline.
// ABOUTME: Defines CompleteHandler, Middleware interface, and MiddlewareFunc adapter.
package llm

import "context"

// CompleteHandler is a function that processes a completion request and returns a response.
// It is used as the unit of composition in the middleware chain.
type CompleteHandler func(ctx context.Context, req *Request) (*Response, error)

// Middleware wraps a CompleteHandler to add cross-cutting behavior (logging, retry, etc.).
type Middleware interface {
	WrapComplete(next CompleteHandler) CompleteHandler
}

// MiddlewareFunc is an adapter that allows ordinary functions to be used as Middleware.
type MiddlewareFunc func(next CompleteHandler) CompleteHandler

// WrapComplete implements the Middleware interface for MiddlewareFunc.
func (f MiddlewareFunc) WrapComplete(next CompleteHandler) CompleteHandler {
	return f(next)
}
