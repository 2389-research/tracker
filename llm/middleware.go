// ABOUTME: Middleware types for the unified LLM client's request/response pipeline.
// ABOUTME: Defines CompleteHandler and the Middleware interface.
package llm

import "context"

// CompleteHandler is a function that processes a completion request and returns a response.
// It is used as the unit of composition in the middleware chain.
type CompleteHandler func(ctx context.Context, req *Request) (*Response, error)

// Middleware wraps a CompleteHandler to add cross-cutting behavior (logging, retry, etc.).
type Middleware interface {
	WrapComplete(next CompleteHandler) CompleteHandler
}
