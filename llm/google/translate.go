// ABOUTME: Request/response translation between the unified llm types and Google Gemini API format.
// ABOUTME: Handles systemInstruction extraction, model role mapping, synthetic tool call IDs, and finish reasons.
package google

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/2389-research/mammoth-lite/llm"
)

// --- Wire format types for the Gemini API ---

// geminiRequest is the wire format for generateContent / streamGenerateContent.
type geminiRequest struct {
	Contents          []geminiContent      `json:"contents"`
	SystemInstruction *geminiContent       `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDecl     `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig    `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenConfig     `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall   `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResp   `json:"functionResponse,omitempty"`
	InlineData       *geminiInlineData     `json:"inlineData,omitempty"`
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
	var sysParts []geminiPart
	var msgs []llm.Message
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem || m.Role == llm.RoleDeveloper {
			for _, part := range m.Content {
				if part.Kind == llm.KindText {
					sysParts = append(sysParts, geminiPart{Text: part.Text})
				}
			}
		} else {
			msgs = append(msgs, m)
		}
	}
	if len(sysParts) > 0 {
		gr.SystemInstruction = &geminiContent{Parts: sysParts}
	}

	// Build contents array.
	for _, m := range msgs {
		content := translateMessageToContent(m)
		if content != nil {
			gr.Contents = append(gr.Contents, *content)
		}
	}

	// Tool definitions.
	if len(req.Tools) > 0 {
		var decls []geminiFuncDecl
		for _, t := range req.Tools {
			decls = append(decls, geminiFuncDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		gr.Tools = []geminiToolDecl{{FunctionDeclarations: decls}}
	}

	// Tool choice.
	if req.ToolChoice != nil {
		gr.ToolConfig = translateToolChoice(req.ToolChoice)
	}

	// Determine if response format requires generation config fields.
	needsResponseFormat := req.ResponseFormat != nil &&
		(req.ResponseFormat.Type == "json_object" || req.ResponseFormat.Type == "json_schema")

	// Generation config.
	if req.Temperature != nil || req.MaxTokens != nil || req.TopP != nil || len(req.StopSequences) > 0 || needsResponseFormat {
		gc := &geminiGenConfig{
			Temperature:   req.Temperature,
			TopP:          req.TopP,
			StopSequences: req.StopSequences,
		}
		if req.MaxTokens != nil {
			gc.MaxOutputTokens = req.MaxTokens
		}
		if needsResponseFormat {
			gc.ResponseMimeType = "application/json"
			if req.ResponseFormat.Type == "json_schema" && len(req.ResponseFormat.JSONSchema) > 0 {
				gc.ResponseSchema = req.ResponseFormat.JSONSchema
			}
		}
		gr.GenerationConfig = gc
	}

	body, err := json.Marshal(gr)
	if err != nil {
		return nil, err
	}

	// Merge provider_options["google"] into the body.
	if opts, ok := req.ProviderOptions["google"]; ok {
		if optsMap, ok := opts.(map[string]any); ok {
			var bodyMap map[string]any
			if err := json.Unmarshal(body, &bodyMap); err != nil {
				return nil, err
			}
			for k, v := range optsMap {
				bodyMap[k] = v
			}
			body, err = json.Marshal(bodyMap)
			if err != nil {
				return nil, err
			}
		}
	}

	return body, nil
}

// translateMessageToContent converts a unified llm.Message to a Gemini content item.
func translateMessageToContent(m llm.Message) *geminiContent {
	role := geminiRole(m.Role)
	var parts []geminiPart

	for _, part := range m.Content {
		switch part.Kind {
		case llm.KindText:
			parts = append(parts, geminiPart{Text: part.Text})
		case llm.KindToolCall:
			if part.ToolCall != nil {
				var args map[string]any
				if len(part.ToolCall.Arguments) > 0 {
					json.Unmarshal(part.ToolCall.Arguments, &args)
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: part.ToolCall.Name,
						Args: args,
					},
				})
			}
		case llm.KindToolResult:
			if part.ToolResult != nil {
				// Gemini uses the function name (not call ID) to match function responses.
				funcName := part.ToolResult.Name
				if funcName == "" {
					funcName = part.ToolResult.ToolCallID
				}
				parts = append(parts, geminiPart{
					FunctionResponse: &geminiFunctionResp{
						Name: funcName,
						Response: map[string]any{
							"content": part.ToolResult.Content,
							"error":   part.ToolResult.IsError,
						},
					},
				})
			}
		// Image content parts can be added when KindImage is defined in the core types.
		}
	}

	if len(parts) == 0 {
		return nil
	}

	return &geminiContent{
		Role:  role,
		Parts: parts,
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
	Candidates    []geminiCandidate  `json:"candidates"`
	UsageMetadata *geminiUsageMeta   `json:"usageMetadata,omitempty"`
	ModelVersion  string             `json:"modelVersion,omitempty"`
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

	var content []llm.ContentPart
	var finishReason string

	if len(gr.Candidates) > 0 {
		candidate := gr.Candidates[0]
		finishReason = candidate.FinishReason

		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				content = append(content, llm.ContentPart{
					Kind: llm.KindText,
					Text: part.Text,
				})
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				content = append(content, llm.ContentPart{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        syntheticID(),
						Name:      part.FunctionCall.Name,
						Arguments: argsJSON,
					},
				})
			}
		}
	}

	var usage llm.Usage
	if gr.UsageMetadata != nil {
		usage = llm.Usage{
			InputTokens:  gr.UsageMetadata.PromptTokenCount,
			OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  gr.UsageMetadata.TotalTokenCount,
		}
	}

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
		panic("mammoth-lite: crypto/rand.Read failed: " + err.Error())
	}
	return "call_" + hex.EncodeToString(b)
}
