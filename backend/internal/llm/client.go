// Package llm implements non-streaming OpenAI-compatible Chat Completions.
// It validates protocol structure only; callers authorize and execute tools.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

var (
	ErrInvalidRequest   = errors.New("llm: invalid request")
	ErrInvalidResponse  = errors.New("llm: invalid response")
	ErrToolCallCount    = errors.New("llm: expected exactly one tool call")
	ErrResponseTooLarge = errors.New("llm: response exceeds size limit")
	ErrAuthentication   = errors.New("llm: authentication failed")
	ErrRateLimit        = errors.New("llm: rate limited")
	ErrProvider         = errors.New("llm: provider failed")
	ErrHTTP             = errors.New("llm: unexpected HTTP status")
	ErrNetwork          = errors.New("llm: network request failed")
)

type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
}

func (configuration Config) Validate() error {
	base, err := url.Parse(configuration.BaseURL)
	if err != nil || base == nil {
		return errors.New("LLM_BASE_URL must be an absolute HTTP(S) API URL")
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.Opaque != "" {
		return errors.New("LLM_BASE_URL must be an absolute HTTP(S) API URL")
	}
	if base.User != nil || base.RawQuery != "" || base.ForceQuery || strings.Contains(configuration.BaseURL, "#") {
		return errors.New("LLM_BASE_URL must not contain credentials, a query, or a fragment")
	}
	if strings.TrimSpace(configuration.Model) == "" {
		return errors.New("LLM_MODEL is required")
	}
	if strings.ContainsAny(configuration.APIKey, "\r\n") {
		return errors.New("LLM_API_KEY must not contain line breaks")
	}
	if configuration.Timeout <= 0 {
		return errors.New("LLM_TIMEOUT must be a positive Go duration")
	}
	return nil
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict,omitempty"`
}

type Request struct {
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	// ToolChoice accepts "auto", "none", "required", or a named function object.
	// An empty value leaves the provider default in effect.
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	// Nil leaves the provider default unchanged; false disables parallel calls.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
	// This local validation flag is never sent to the provider.
	RequireExactlyOneToolCall bool `json:"-"`
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Response struct {
	Content   *string
	ToolCalls []ToolCall
	// Callers must inspect length/content_filter before using text as a complete answer.
	FinishReason string
}

// HTTPError exposes only the status code; provider bodies and headers are discarded.
type HTTPError struct {
	StatusCode int
}

func (err *HTTPError) Error() string {
	return fmt.Sprintf("%s (HTTP %d)", err.Unwrap(), err.StatusCode)
}

func (err *HTTPError) Unwrap() error {
	switch {
	case err.StatusCode == http.StatusUnauthorized || err.StatusCode == http.StatusForbidden:
		return ErrAuthentication
	case err.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimit
	case err.StatusCode >= 500 && err.StatusCode <= 599:
		return ErrProvider
	default:
		return ErrHTTP
	}
}

type Client struct {
	endpoint   string
	model      string
	apiKey     string
	httpClient *http.Client
}

func New(configuration Config) (*Client, error) {
	if err := configuration.Validate(); err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(configuration.BaseURL, "/") + "/chat/completions"
	httpClient := &http.Client{
		Timeout: configuration.Timeout,
		// Redirects could forward prompts or credentials to another endpoint.
		CheckRedirect: rejectRedirect,
	}

	return &Client{
		endpoint:   endpoint,
		model:      configuration.Model,
		apiKey:     configuration.APIKey,
		httpClient: httpClient,
	}, nil
}

func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func (client *Client) Complete(ctx context.Context, request Request) (Response, error) {
	logger := slog.Default().With("component", "llm")
	started := time.Now()
	logger.InfoContext(ctx, "LLM request started", "message_count", len(request.Messages), "tool_count", len(request.Tools))

	response, err := client.complete(ctx, request)
	logger = logger.With("duration_ms", time.Since(started).Milliseconds())
	if err != nil {
		if errors.Is(err, context.Canceled) {
			logger.InfoContext(ctx, "LLM request canceled")
		} else {
			// complete returns only sanitized errors, never raw transport/provider details.
			var httpError *HTTPError
			if errors.As(err, &httpError) {
				logger = logger.With("status_code", httpError.StatusCode)
			}
			var responseError *ResponseError
			if errors.As(err, &responseError) {
				logger = logger.With("finish_reason", responseError.FinishReason, "tool_call_count", responseError.ToolCallCount)
			}
			logger.ErrorContext(ctx, "LLM request failed", "error", err)
		}
		return response, err
	}
	logger.InfoContext(ctx, "LLM request completed", "finish_reason", response.FinishReason, "tool_call_count", len(response.ToolCalls))
	return response, nil
}

// complete keeps request/response validation inside the logging boundary so every
// attempt produces one terminal event without exposing partial or unvalidated data.
func (client *Client) complete(ctx context.Context, request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}

	httpRequest, err := client.newHTTPRequest(ctx, request)
	if err != nil {
		return Response{}, err
	}

	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return Response{}, transportError(ctx, err)
	}
	// Close errors cannot change the completed read/status result. Never log them.
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode != http.StatusOK {
		return Response{}, &HTTPError{StatusCode: httpResponse.StatusCode}
	}

	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return Response{}, transportError(ctx, err)
	}
	if len(responseBody) > maxResponseBytes {
		return Response{}, ErrResponseTooLarge
	}

	return parseResponse(responseBody, request.RequireExactlyOneToolCall)
}

func (client *Client) newHTTPRequest(ctx context.Context, request Request) (*http.Request, error) {
	payload := struct {
		Model string `json:"model"`
		Request
	}{
		Model:   client.model,
		Request: request,
	}
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, ErrInvalidRequest
	}

	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if client.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+client.apiKey)
	}

	return httpRequest, nil
}

func transportError(ctx context.Context, err error) error {
	// Raw transport errors can include URLs, credentials, or remote text.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return context.DeadlineExceeded
	}
	return ErrNetwork
}

func validateRequest(request Request) error {
	if len(request.Messages) == 0 {
		return ErrInvalidRequest
	}
	for _, message := range request.Messages {
		if message.Role != "system" && message.Role != "user" {
			return ErrInvalidRequest
		}
	}
	for _, tool := range request.Tools {
		if tool.Type != "function" {
			return ErrInvalidRequest
		}
		if strings.TrimSpace(tool.Function.Name) == "" {
			return ErrInvalidRequest
		}
		if !jsonObject(tool.Function.Parameters) {
			return ErrInvalidRequest
		}
	}
	if len(request.ToolChoice) > 0 && !validToolChoice(request.ToolChoice) {
		return ErrInvalidRequest
	}
	return nil
}

func validToolChoice(value json.RawMessage) bool {
	var mode string
	if json.Unmarshal(value, &mode) == nil {
		return mode == "auto" || mode == "none" || mode == "required"
	}
	var namedChoice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(value, &namedChoice); err != nil {
		return false
	}
	if namedChoice.Type != "function" {
		return false
	}
	return strings.TrimSpace(namedChoice.Function.Name) != ""
}

func jsonObject(value []byte) bool {
	value = bytes.TrimSpace(value)
	return len(value) > 0 && value[0] == '{' && json.Valid(value)
}
