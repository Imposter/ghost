package access

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Webhook calls the external authorizer over HTTP.
type Webhook struct {
	url    string
	secret []byte
	client *http.Client
	now    func() time.Time
}

// NewWebhook returns a Webhook posting to baseURL + AuthorizePath. client may
// be nil (a client with timeout is created); now may be nil.
func NewWebhook(baseURL string, secret []byte, timeout time.Duration, client *http.Client, now func() time.Time) *Webhook {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	if now == nil {
		now = time.Now
	}
	return &Webhook{
		url:    strings.TrimRight(baseURL, "/") + AuthorizePath,
		secret: secret,
		client: client,
		now:    now,
	}
}

// Authorize signs req (setting TS and Nonce) and posts it. Any transport
// error, non-200 status, or undecodable body is returned as an error.
func (w *Webhook) Authorize(ctx context.Context, req Request) (Decision, error) {
	req.TS = w.now().Unix()
	req.Nonce = NewNonce()
	body, err := json.Marshal(req)
	if err != nil {
		return Decision{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return Decision{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(SignatureHeader, Sign(w.secret, body))

	resp, err := w.client.Do(httpReq)
	if err != nil {
		return Decision{}, fmt.Errorf("authorizer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return Decision{}, fmt.Errorf("authorizer: status %d", resp.StatusCode)
	}
	var d Decision
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&d); err != nil {
		return Decision{}, fmt.Errorf("authorizer: decode: %w", err)
	}
	return d, nil
}
