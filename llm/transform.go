// ABOUTME: Transform middleware for modifying LLM requests and responses in the pipeline.
// ABOUTME: Supports request transforms (before call) and optional response transforms (after call).
package llm

import "context"

// RequestTransformFunc modifies a Request in place before it is sent downstream.
type RequestTransformFunc func(req *Request)

// ResponseTransformFunc modifies a Response in place after it is received from downstream.
type ResponseTransformFunc func(resp *Response)

// TransformOption configures a TransformMiddleware.
type TransformOption func(*TransformMiddleware)

// TransformMiddleware applies request and/or response transforms around a CompleteHandler.
type TransformMiddleware struct {
	requestTransform  RequestTransformFunc
	responseTransform ResponseTransformFunc
}

// WithResponseTransform adds a response transform that runs after a successful downstream call.
func WithResponseTransform(fn ResponseTransformFunc) TransformOption {
	return func(tm *TransformMiddleware) {
		tm.responseTransform = fn
	}
}

// NewTransformMiddleware creates a TransformMiddleware with the given request transform
// and optional TransformOptions. If reqFn is nil, the request is passed through unchanged.
func NewTransformMiddleware(reqFn RequestTransformFunc, opts ...TransformOption) *TransformMiddleware {
	tm := &TransformMiddleware{
		requestTransform: reqFn,
	}
	for _, opt := range opts {
		opt(tm)
	}
	return tm
}

// WrapComplete implements the Middleware interface. It applies the request transform
// before calling next, and the response transform after a successful call.
func (tm *TransformMiddleware) WrapComplete(next CompleteHandler) CompleteHandler {
	return func(ctx context.Context, req *Request) (*Response, error) {
		if tm.requestTransform != nil {
			tm.requestTransform(req)
		}

		resp, err := next(ctx, req)
		if err != nil {
			return nil, err
		}

		if tm.responseTransform != nil {
			tm.responseTransform(resp)
		}

		return resp, nil
	}
}
