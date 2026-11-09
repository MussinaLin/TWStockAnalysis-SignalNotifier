package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ interface {
	Send(ctx context.Context, chatID, text string) error
} = Client{}

func serve(t *testing.T, status int, body string) Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	c := New("123:abc")
	c.apiBase = srv.URL
	return c
}

func TestSendPostsMessage(t *testing.T) {
	var path, ct, got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ct = r.URL.Path, r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	c := New("123:abc")
	c.apiBase = srv.URL
	require.NoError(t, c.Send(context.Background(), "987", "hi"))
	assert.Equal(t, "/bot123:abc/sendMessage", path)
	assert.Equal(t, "application/json", ct)
	assert.JSONEq(t, `{"chat_id":"987","text":"hi"}`, got)
}

func TestSendBlocked(t *testing.T) {
	c := serve(t, 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	assert.True(t, IsBlocked(c.Send(context.Background(), "1", "x")))
	c = serve(t, 403, `{"ok":false,"error_code":403,"description":"Forbidden: user is deactivated"}`)
	assert.True(t, IsBlocked(c.Send(context.Background(), "1", "x")))
	c = serve(t, 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation"}`)
	assert.False(t, IsBlocked(c.Send(context.Background(), "1", "x")))
	assert.False(t, IsBlocked(nil))
}

func TestSendRetryAfter(t *testing.T) {
	c := serve(t, 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`)
	d, ok := RetryAfter(c.Send(context.Background(), "1", "x"))
	assert.True(t, ok)
	assert.Equal(t, 3*time.Second, d)
	_, ok = RetryAfter(nil)
	assert.False(t, ok)
}

func TestSendNonJSONBody(t *testing.T) {
	c := serve(t, 502, `<html>bad gateway</html>`)
	err := c.Send(context.Background(), "1", "x")
	var ae *APIError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, 502, ae.StatusCode)
}

func TestSendErrorDoesNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c := New("123:secret-token")
	c.apiBase = srv.URL
	srv.Close()
	err := c.Send(context.Background(), "1", "x")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-token")
}

func TestSendBuildErrorDoesNotLeakToken(t *testing.T) {
	c := New("123:secret-token\n")
	err := c.Send(context.Background(), "1", "x")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-token")
}
