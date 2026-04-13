// ABOUTME: Request/response translation between the unified llm types and Google Gemini API format.
// ABOUTME: Handles systemInstruction extraction, model role mapping, synthetic tool call IDs, and finish reasons.
package google

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// --- Wire format types for the Gemini API ---

// geminiRequest is the wire format for generateContent / streamGenerateContent.
type geminiRequest struct {
	Contents          []geminiContent   `json:"contents"`
	SystemInstruction *geminiContent    `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDecl  `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenConfig  `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResp `json:"functionResponse,omitempty"`
	InlineData       *geminiInlineData   `json:"inlineData,omitempty"`
	ThoughtSignature string              `json:"thoughtSignature,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiToolChoiceConfig `json:"functionCallingConfig,omitempty"`
}

type geminiToolChoiceConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiGenConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
}

// translateRequest converts a unified llm.Request to Gemini API JSON.
func translateRequest(req *llm.Request) ([]byte, error) {
	gr := geminiRequest{}

	// Extract system/developer messages into systemInstruction.
	gr.SystemInstruction, gr.Contents = extractSystemAndContents(req.Messages)

	// Tool definitions.
	gr.Tools = translateGeminiTools(req.Tools)

	// Tool choice.
	if req.ToolChoice != nil {
		gr.ToolConfig = translateToolChoice(req.ToolChoice)
	}

	// Generation config.
	gr.GenerationConfig = buildGenerationConfig(req)

	body, err := json.Marshal(gr)
	if err != nil {
		return nil, err
	}

	return mergeProviderOptions(body, req.ProviderOptions, "gemini")
}

// extractSystemAndContents separates system/developer messages into a
// systemInstruction and converts remaining messages to Gemini contents.
func extractSystemAndContents(messages []llm.Message) (*geminiContent, []geminiContent) {
	var sysParts []geminiPart
	var contents []geminiContent
	for _, m := range messages {
		if m.Role == llm.RoleSystem || m.Role == llm.RoleDeveloper {
			sysParts = append(sysParts, extractSystemParts(m)...)
		} else {
			if content := translateMessageToContent(m); content != nil {
				contents = append(contents, *content)
			}
		}
	}
	if len(sysParts) == 0 {
		return nil, contents
	}
	return &geminiContent{Parts: sysParts}, contents
}

// extractSystemParts collects text parts from a system/developer message.
func extractSystemParts(m llm.Message) []geminiPart {
	var parts []geminiPart
	for _, part := range m.Content {
		if part.Kind == llm.KindText {
			parts = append(parts, geminiPart{Text: part.Text})
		}
	}
	return parts
}

// translateGeminiTools converts unified tool definitions to Gemini format.
func translateGeminiTools(tools []llm.ToolDefinition) []geminiToolDecl {
	if len(tools) == 0 {
		return nil
	}
	var decls []geminiFuncDecl
	for _, t := range tools {
		decls = append(decls, geminiFuncDecl{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}
}

// buildGenerationConfig creates a Gemini generation config from the request fields.
func buildGenerationConfig(req *llm.Request) *geminiGenConfig {
	needsResponseFormat := responseFormatRequired(req)

	if req.Temperature == nil && req.MaxTokens == nil && req.TopP == nil && len(req.StopSequences) == 0 && !needsResponseFormat {
		return nil
	}

	gc := &geminiGenConfig{
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.StopSequences,
	}
	if req.MaxTokens != nil {
		gc.MaxOutputTokens = req.MaxTokens
	}
	if needsResponseFormat {
		applyResponseFormat(gc, req.ResponseFormat)
	}
	return gc
}

// responseFormatRequired returns true when the request specifies a JSON response format.
func responseFormatRequired(req *llm.Request) bool {
	return req.ResponseFormat != nil &&
		(req.ResponseFormat.Type == "json_object" || req.ResponseFormat.Type == "json_schema")
}

// applyResponseFormat sets ResponseMimeType and optionally ResponseSchema on the config.
func applyResponseFormat(gc *geminiGenConfig, rf *llm.ResponseFormat) {
	gc.ResponseMimeType = "application/json"
	if rf.Type == "json_schema" && len(rf.JSONSchema) > 0 {
		gc.ResponseSchema = rf.JSONSchema
	}
}

// mergeProviderOptions merges provider-specific options into the JSON body.
func mergeProviderOptions(body []byte, providerOpts map[string]any, providerKey string) ([]byte, error) {
	opts, ok := providerOpts[providerKey]
	if !ok {
		return body, nil
	}
	optsMap, ok := opts.(map[string]any)
	if !ok {
		return body, nil
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, err
	}
	for k, v := range optsMap {
		bodyMap[k] = v
	}
	return json.Marshal(bodyMap)
}

// translateMessageToContent converts a unified llm.Message to a Gemini content item.
func translateMessageToContent(m llm.Message) *geminiContent {
	role := geminiRole(m.Role)
	parts := translateContentParts(m.Content)
	if len(parts) == 0 {
		return nil
	}
	return &geminiContent{Role: role, Parts: parts}
}

// translateContentParts converts a slice of unified content parts to Gemini parts.
func translateContentParts(content []llm.ContentPart) []geminiPart {
	var parts []geminiPart
	for _, part := range content {
		if p := translateSingleContentPart(part); p != nil {
			parts = append(parts, *p)
		}
	}
	return parts
}

// translateSingleContentPart converts a single unified ContentPart to a Gemini part.
// Returns nil for unsupported or empty parts.
func translateSingleContentPart(part llm.ContentPart) *geminiPart {
	switch part.Kind {
	case llm.KindText:
		p := geminiPart{Text: part.Text}
		return &p
	case llm.KindToolCall:
		return translateToolCallPart(part)
	case llm.KindToolResult:
		return translateToolResultPart(part)
	}
	// Image content parts can be added when KindImage is defined in the core types.
	return nil
}

// translateToolCallPart converts a tool call content part to a Gemini function call part.
func translateToolCallPart(part llm.ContentPart) *geminiPart {
	if part.ToolCall == nil {
		return nil
	}
	var args map[string]any
	if len(part.ToolCall.Arguments) > 0 {
		json.Unmarshal(part.ToolCall.Arguments, &args)
	}
	return &geminiPart{
		FunctionCall: &geminiFunctionCall{
			Name: part.ToolCall.Name,
			Args: args,
		},
		ThoughtSignature: part.ToolCall.ThoughtSigData,
	}
}

// translateToolResultPart converts a tool result content part to a Gemini function response part.
func translateToolResultPart(part llm.ContentPart) *geminiPart {
	if part.ToolResult == nil {
		return nil
	}
	// Gemini uses the function name (not call ID) to match function responses.
	funcName := part.ToolResult.Name
	if funcName == "" {
		funcName = part.ToolResult.ToolCallID
	}
	return &geminiPart{
		FunctionResponse: &geminiFunctionResp{
			Name: funcName,
			Response: map[string]any{
				"content": part.ToolResult.Content,
				"error":   part.ToolResult.IsError,
			},
		},
	}
}

// geminiRole maps unified roles to Gemini roles.
func geminiRole(role llm.Role) string {
	switch role {
	case llm.RoleUser, llm.RoleTool:
		return "user"
	case llm.RoleAssistant:
		return "model"
	default:
		return string(role)
	}
}

// translateToolChoice converts llm.ToolChoice to Gemini toolConfig format.
func translateToolChoice(tc *llm.ToolChoice) *geminiToolConfig {
	switch tc.Mode {
	case "auto":
		return &geminiToolConfig{FunctionCallingConfig: &geminiToolChoiceConfig{Mode: "AUTO"}}
	case "none":
		return &geminiToolConfig{FunctionCallingConfig: &geminiToolChoiceConfig{Mode: "NONE"}}
	case "required":
		return &geminiToolConfig{FunctionCallingConfig: &geminiToolChoiceConfig{Mode: "ANY"}}
	case "named":
		return &geminiToolConfig{FunctionCallingConfig: &geminiToolChoiceConfig{
			Mode:                 "ANY",
			AllowedFunctionNames: []string{tc.ToolName},
		}}
	default:
		return nil
	}
}

// --- Response translation ---

// geminiResponse is the wire format for a Gemini API response.
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsageMeta  `json:"usageMetadata,omitempty"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiUsageMeta struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// translateResponse converts Gemini API JSON to a unified llm.Response.
func translateResponse(raw []byte) (*llm.Response, error) {
	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, err
	}

	content, finishReason := extractCandidateContent(gr.Candidates)
	usage := extractUsage(gr.UsageMetadata)
	fr := translateFinishReason(finishReason, hasToolCalls(content))

	return &llm.Response{
		ID:    "",
		Model: gr.ModelVersion,
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: content,
		},
		FinishReason: fr,
		Usage:        usage,
		Raw:          raw,
	}, nil
}

// extractCandidateContent pulls content parts and finish reason from the first candidate.
func extractCandidateContent(candidates []geminiCandidate) ([]llm.ContentPart, string) {
	if len(candidates) == 0 {
		return nil, ""
	}
	candidate := candidates[0]
	var content []llm.ContentPart
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			content = append(content, llm.ContentPart{Kind: llm.KindText, Text: part.Text})
		}
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			content = append(content, llm.ContentPart{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:             syntheticID(),
					Name:           part.FunctionCall.Name,
					Arguments:      argsJSON,
					ThoughtSigData: part.ThoughtSignature,
				},
			})
		}
	}
	return content, candidate.FinishReason
}

// extractUsage converts Gemini usage metadata to the unified Usage struct.
func extractUsage(meta *geminiUsageMeta) llm.Usage {
	if meta == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		InputTokens:  meta.PromptTokenCount,
		OutputTokens: meta.CandidatesTokenCount,
		TotalTokens:  meta.TotalTokenCount,
	}
}

func hasToolCalls(parts []llm.ContentPart) bool {
	for _, p := range parts {
		if p.Kind == llm.KindToolCall {
			return true
		}
	}
	return false
}

// translateFinishReason maps Gemini finish reasons to the unified format.
func translateFinishReason(reason string, hasFunctionCalls bool) llm.FinishReason {
	if hasFunctionCalls {
		return llm.FinishReason{Reason: "tool_calls", Raw: reason}
	}

	var mapped string
	switch reason {
	case "STOP":
		mapped = "stop"
	case "MAX_TOKENS":
		mapped = "length"
	case "SAFETY":
		mapped = "content_filter"
	case "RECITATION":
		mapped = "content_filter"
	default:
		mapped = strings.ToLower(reason)
	}

	return llm.FinishReason{Reason: mapped, Raw: reason}
}

// syntheticID generates a short random hex ID for Gemini tool calls (which lack native IDs).
func syntheticID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("tracker: crypto/rand.Read failed: " + err.Error())
	}
	return "call_" + hex.EncodeToString(b)
}
