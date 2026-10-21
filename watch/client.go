package watch

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client talks to an instance's intel channel API with a paired
// device credential: the token names the device, and every request
// is signed with its private key (keys.go), so the token alone
// cannot be used from anywhere else.
type Client struct {
	Server string // "https://evesynapse.example"
	Token  string
	Key    ed25519.PrivateKey
	HTTP   *http.Client
	Agent  string
}

// Channel is one channel the token's account can read.
type Channel struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Post bool   `json:"post"`
}

// PostResult is what a batch of lines came to.
type PostResult struct {
	Posted     int `json:"posted"`
	Duplicates int `json:"duplicates"`
	Skipped    int `json:"skipped"`
}

// APIError is a refusal from the server: its status and message.
// Retry says whether sending again later may succeed.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("server said %d: %s", e.Status, e.Message) }

// Retry: the server was down, busy, or asked us to slow down.
func (e *APIError) Retry() bool { return e.Status >= 500 || e.Status == http.StatusTooManyRequests }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Server, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if c.Key != nil {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		sig := ed25519.Sign(c.Key, []byte(SignText(method, path, ts, data)))
		req.Header.Set("X-Axon-Timestamp", ts)
		req.Header.Set("X-Axon-Signature", base64.StdEncoding.EncodeToString(sig))
	}
	req.Header.Set("Accept", "application/json")
	if c.Agent != "" {
		req.Header.Set("User-Agent", c.Agent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	return json.Unmarshal(data, out)
}

// Channels lists the account's channels, and its name.
func (c *Client) Channels(ctx context.Context) (account string, list []Channel, err error) {
	var out struct {
		Account  string    `json:"account"`
		Channels []Channel `json:"channels"`
	}
	err = c.do(ctx, http.MethodGet, "/api/intel/v1/channels", nil, &out)
	return out.Account, out.Channels, err
}

// PostLines sends chat lines to a channel.
func (c *Client) PostLines(ctx context.Context, channel int64, lines []string) (PostResult, error) {
	var out PostResult
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/intel/v1/channels/%d/lines", channel), map[string]any{"lines": lines}, &out)
	return out, err
}
