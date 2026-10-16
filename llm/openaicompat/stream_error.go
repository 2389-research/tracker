// ABOUTME: Reads an error carried inside a 200 SSE stream chunk (OpenAI-compatible servers).
// ABOUTME: Surfaces every substantive error shape, ignores placeholder "no error" values.
package openaicompat

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/2389-research/tracker/llm"
)

// chatStreamError is a chunk carrying an error in a 200 SSE stream. The value
// stays raw so a shape other than chatStreamErrorDetail still surfaces.
type chatStreamError struct {
	Error json.RawMessage `json:"error"`
}

// chatStreamErrorDetail is the usual shape of a stream chunk's error object.
// Status stands in for a missing code: some servers send only an HTTP status
// (`{"status":401,"detail":"Unauthorized"}`) or a Google-style status name.
type chatStreamErrorDetail struct {
	Message string              `json:"message"`
	Type    string              `json:"type"`
	Code    llm.StreamErrorCode `json:"code"`
	Status  llm.StreamErrorCode `json:"status"`
}

// streamErrorClass is the part of an error object that classifies it, read
// best effort from a shape that is not the usual one.
type streamErrorClass struct {
	Type   string              `json:"type"`
	Code   llm.StreamErrorCode `json:"code"`
	Status llm.StreamErrorCode `json:"status"`
}

// code returns the error's code, else its status.
func (c streamErrorClass) code() string {
	if c.Code != "" {
		return string(c.Code)
	}
	return string(c.Status)
}

// tryEmitSSEError attempts to parse and emit an error embedded in the SSE stream.
// Returns true if an error was found and emitted. A non-null `error` value that
// does not decode as the usual object (a bare string, a non-string message), or
// an object with only other fields (`detail`, `status`), is still emitted,
// naming the raw value, so it is never read as an empty normal chunk that lets
// the stream end as a success. A placeholder value (isEmptyErrorValue: `{}`,
// `""`, `false`, `0`, `[]`, or an object whose fields are all such values) is
// no error: some servers send it on every successful chunk. A payload that is
// not a JSON object (an array-wrapped error, a truncated chunk) is emitted too
// (undecodableErrorChunk), so it is classified, not a bare parse error the
// retry middleware never retries.
func (a *Adapter) tryEmitSSEError(data string, ch chan<- llm.StreamEvent) bool {
	var errChunk chatStreamError
	if err := json.Unmarshal([]byte(data), &errChunk); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: undecodableErrorChunk(data)}
		return true
	}
	raw := bytes.TrimSpace(errChunk.Error)
	if isEmptyErrorValue(raw) {
		return false
	}
	ch <- llm.StreamEvent{Type: llm.EventError, Err: streamErrorFromRaw(raw)}
	return true
}

// isEmptyErrorValue reports whether a chunk's `error` value says "no error":
// absent, null, `""`, `false`, `0`, `[]`, `{}`, or an object or array made
// only of such values (`{"message":null,"code":null}`, `{"message":""}`).
// Anything else, a non-empty string or any field with content, is an error.
func isEmptyErrorValue(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return isEmptyJSON(v)
}

// isEmptyJSON reports whether a decoded JSON value is null, a zero scalar, or
// a container of only such values.
func isEmptyJSON(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return allEmpty(x)
	case []any:
		return allEmptySlice(x)
	}
	return v == nil || v == "" || v == false || v == float64(0)
}

// allEmpty reports whether every value in m is empty (isEmptyJSON).
func allEmpty(m map[string]any) bool {
	for _, v := range m {
		if !isEmptyJSON(v) {
			return false
		}
	}
	return true
}

// allEmptySlice reports whether every element of s is empty (isEmptyJSON).
func allEmptySlice(s []any) bool {
	for _, v := range s {
		if !isEmptyJSON(v) {
			return false
		}
	}
	return true
}

// streamErrorFromRaw builds the error for a chunk's substantive `error` value:
// the usual object, then a bare string, then the raw text with whatever code
// (or status) and type can be read from it. An object with no message, code
// or type, but other fields (`{"detail":...}`, `{"status":...}`), names its
// raw text.
func streamErrorFromRaw(raw json.RawMessage) error {
	var d chatStreamErrorDetail
	if json.Unmarshal(raw, &d) == nil {
		cls := streamErrorClass{Type: d.Type, Code: d.Code, Status: d.Status}
		msg := d.Message
		if msg == "" && d.Code == "" && d.Type == "" {
			msg = string(raw) // only other fields: their text is the server's words
		}
		return compatStreamError(cls.code(), d.Type, msg)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return compatStreamError("", "", s)
	}
	var cls streamErrorClass
	_ = json.Unmarshal(raw, &cls) // best effort: a non-string type leaves it empty
	return compatStreamError(cls.code(), cls.Type, string(raw))
}

// undecodableErrorChunk builds the error for a chunk that mentions "error" but
// is not a JSON object: its raw text is the message, so the server's words and
// any billing or usage-limit signal reach the log and the classifier, and the
// code and type are read, best effort, from the first element of an
// array-wrapped error (`[{"error":{...}}]`, as Google-style servers send).
func undecodableErrorChunk(data string) error {
	var arr []chatStreamError
	var cls streamErrorClass
	if json.Unmarshal([]byte(data), &arr) == nil && len(arr) > 0 {
		_ = json.Unmarshal(arr[0].Error, &cls) // best effort: an odd shape leaves it empty
	}
	return compatStreamError(cls.code(), cls.Type, data)
}

// compatStreamError builds a classified stream error whose text names the code
// and type, so the log shows what the server sent (#684).
func compatStreamError(code, errType, msg string) error {
	if msg == "" {
		// Some providers omit the message; synthesize one so the error is not
		// silently dropped as a normal chunk.
		msg = "stream error"
	}
	if code != "" || errType != "" {
		msg += fmt.Sprintf(" (code=%q, type=%q)", code, errType)
	}
	return llm.ErrorFromOpenAIStreamError(code, errType, "openai-compat: "+msg, "openai-compat")
}
