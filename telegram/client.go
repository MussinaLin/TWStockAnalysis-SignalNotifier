// Package telegram is a minimal Telegram Bot API client for sending messages.
package telegram

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.telegram.org"

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Client sends plain-text messages through the Telegram Bot API.
type Client struct {
	Token   string
	apiBase string // overridden in tests; defaults to defaultAPIBase
}

// New returns a Client for the given bot token.
func New(token string) Client {
	return Client{Token: token}
}

// APIError is a non-OK response from the Telegram Bot API.
type APIError struct {
	StatusCode  int
	Description string
	RetryAfter  int // parameters.retry_after, seconds
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: status %d: %s", e.StatusCode, e.Description)
}

// IsBlocked reports whether err means the user blocked the bot or the account is deactivated.
func IsBlocked(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusForbidden {
		return false
	}
	return strings.Contains(ae.Description, "blocked") || strings.Contains(ae.Description, "deactivated")
}

// RetryAfter reports the wait requested by a 429 response.
func RetryAfter(err error) (time.Duration, bool) {
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	return time.Duration(ae.RetryAfter) * time.Second, true
}

// stripURL removes *url.Error wrapping, whose message embeds the URL (and token).
func stripURL(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}

// Send posts text to chatID without a parse_mode.
func (c Client) Send(ctx context.Context, chatID, text string) error {
	body, err := json.Marshal(map[string]string{"chat_id": chatID, "text": text})
	if err != nil {
		return err
	}
	endpoint := cmp.Or(c.apiBase, defaultAPIBase) + "/bot" + c.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", stripURL(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendMessage: %w", stripURL(err))
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode == http.StatusOK && result.OK {
		return nil
	}
	return &APIError{StatusCode: resp.StatusCode, Description: result.Description, RetryAfter: result.Parameters.RetryAfter}
}
