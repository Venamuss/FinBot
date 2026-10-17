package llm

import (
	"encoding/json"
	"strings"
)

// Wire arguments are a JSON-encoded string; callers receive the decoded JSON
// bytes without converting numbers through float64 or interpreting tool fields.
type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string  `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"function"`
}

func parseResponse(body []byte, requireExactlyOne bool) (Response, error) {
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Message struct {
				Role      string         `json:"role"`
				Content   *string        `json:"content"`
				ToolCalls []wireToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Response{}, ErrInvalidResponse
	}
	// No n is requested: multiple choices would be ambiguous to the caller.
	if len(envelope.Choices) != 1 || len(envelope.Error) != 0 {
		return Response{}, ErrInvalidResponse
	}
	choice := envelope.Choices[0]

	if choice.Message.Role != "assistant" {
		return Response{}, ErrInvalidResponse
	}

	switch choice.FinishReason {
	case "tool_calls":
		if len(choice.Message.ToolCalls) == 0 {
			return Response{}, ErrInvalidResponse
		}
	case "stop", "length", "content_filter":
		if len(choice.Message.ToolCalls) != 0 {
			return Response{}, ErrInvalidResponse
		}
	default:
		return Response{}, ErrInvalidResponse
	}
	if choice.Message.Content == nil && len(choice.Message.ToolCalls) == 0 && choice.FinishReason == "stop" {
		return Response{}, ErrInvalidResponse
	}

	response := Response{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
	}
	seenIDs := make(map[string]bool)
	for _, call := range choice.Message.ToolCalls {
		if call.Type != "function" || strings.TrimSpace(call.ID) == "" || seenIDs[call.ID] {
			return Response{}, ErrInvalidResponse
		}
		if strings.TrimSpace(call.Function.Name) == "" || call.Function.Arguments == nil {
			return Response{}, ErrInvalidResponse
		}
		arguments := json.RawMessage(*call.Function.Arguments)
		if !jsonObject(arguments) {
			return Response{}, ErrInvalidResponse
		}
		seenIDs[call.ID] = true
		response.ToolCalls = append(response.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: arguments,
		})
	}
	if requireExactlyOne && len(response.ToolCalls) != 1 {
		return Response{}, ErrToolCallCount
	}
	return response, nil
}
