package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

var ErrAPI = errors.New("Telegram API request failed")

// requestError retains only safe protocol metadata, never remote descriptions or URLs.
type requestError struct {
	reason     string
	httpStatus int
	apiCode    int
}

func (err *requestError) Error() string {
	return ErrAPI.Error()
}

func (err *requestError) Unwrap() error {
	return ErrAPI
}

type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

type Message struct {
	From *User  `json:"from"`
	Chat Chat   `json:"chat"`
	Date int64  `json:"date"`
	Text string `json:"text"`
}

type User struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Client struct {
	endpoint   string
	httpClient *http.Client
	retryDelay time.Duration
}

func New(token string) *Client {
	return &Client{
		endpoint: "https://api.telegram.org/bot" + token + "/",
		httpClient: &http.Client{
			Timeout: 40 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		retryDelay: time.Second,
	}
}

func (client *Client) Updates(ctx context.Context, offset int64) ([]Update, error) {
	slog.DebugContext(ctx, "Telegram poll requested", "component", "telegram", "offset", offset)
	request := struct {
		Offset  int64    `json:"offset"`
		Timeout int      `json:"timeout"`
		Allowed []string `json:"allowed_updates"`
	}{offset, 25, []string{"message"}}
	var updates []Update
	err := client.call(ctx, "getUpdates", request, &updates)
	return updates, err
}

func (client *Client) Send(ctx context.Context, chatID int64, text string) error {
	request := struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}{chatID, text}
	return client.call(ctx, "sendMessage", request, nil)
}

func (client *Client) call(ctx context.Context, method string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return ErrAPI
	}
	for attempt := 0; attempt < 3; attempt++ {
		retry, err := client.request(ctx, method, body, output)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var failure *requestError
		if errors.As(err, &failure) {
			slog.WarnContext(ctx, "Telegram API request failed", "component", "telegram",
				"method", method, "attempt", attempt+1, "reason", failure.reason,
				"http_status", failure.httpStatus, "api_error_code", failure.apiCode, "retryable", retry)
		}
		if !retry || attempt == 2 {
			return err
		}
		if err := wait(ctx, client.retryDelay*time.Duration(1<<attempt)); err != nil {
			return err
		}
	}
	return ErrAPI
}

func (client *Client) request(ctx context.Context, method string, body []byte, output any) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+method, bytes.NewReader(body))
	if err != nil {
		return false, &requestError{reason: "invalid_request"}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		// Transport errors include the token-bearing URL and must never escape.
		reason := "network"
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			reason = "timeout"
		}
		return true, &requestError{reason: reason}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode == 429 || response.StatusCode >= 500, &requestError{reason: "http_status", httpStatus: response.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return false, &requestError{reason: "response_read", httpStatus: response.StatusCode}
	}
	if len(data) > 1<<20 {
		return false, &requestError{reason: "response_too_large", httpStatus: response.StatusCode}
	}
	var envelope struct {
		OK        bool            `json:"ok"`
		Result    json.RawMessage `json:"result"`
		ErrorCode int             `json:"error_code"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return false, &requestError{reason: "invalid_response", httpStatus: response.StatusCode}
	}
	if !envelope.OK {
		return envelope.ErrorCode == 429 || envelope.ErrorCode >= 500, &requestError{reason: "api_error", httpStatus: response.StatusCode, apiCode: envelope.ErrorCode}
	}
	if len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) {
		return false, &requestError{reason: "invalid_response", httpStatus: response.StatusCode}
	}
	if output != nil && json.Unmarshal(envelope.Result, output) != nil {
		return false, &requestError{reason: "invalid_response", httpStatus: response.StatusCode}
	}
	return false, nil
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
