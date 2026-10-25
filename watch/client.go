package watch

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// maxAttempts bounds the retries of one call. A caller's own
// pacing (the watcher's backoff) sits above this; these retries are
// for the failures that heal in a second or two, above all the
// VPN's intermittent DNS.
const maxAttempts = 3

// retryWaits is the pause before the second and third attempts.
var retryWaits = []time.Duration{500 * time.Millisecond, 1 * time.Second}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	hc := c.HTTP
	if hc == nil {
		hc = siteHTTPClient()
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			wait := retryWaits[attempt-1]
			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(wait):
			}
		}
		err := c.doOnce(ctx, hc, method, path, data, body != nil, out)
		if err == nil {
			return nil
		}
		lastErr = err
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			// The server answered; whether trying again later helps
			// is the caller's call (APIError.Retry), not ours.
			return err
		}
		if !transientErr(ctx, err) {
			return err
		}
	}
	return lastErr
}

// transientErr says whether a failed attempt got no answer from the
// server for a reason that may heal: a DNS failure, a timeout, a
// reset connection. Certificate failures are not transient — the
// server (or something claiming to be it) is wrong, not unreachable.
func transientErr(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certVerifyErr *tls.CertificateVerificationError
	return !errors.As(err, &unknownAuthority) &&
		!errors.As(err, &hostnameErr) &&
		!errors.As(err, &certVerifyErr)
}

// doOnce makes one attempt. The request is built and signed here,
// per attempt, so a retry carries a fresh timestamp: the site
// treats each signature as single-use, and a stale one would be
// refused. Retrying a post is safe for the same reason the watcher
// can requeue a batch: the server stores each line once and counts
// repeats as duplicates.
func (c *Client) doOnce(ctx context.Context, hc *http.Client, method, path string, data []byte, hasBody bool, out any) error {
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
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respData, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(respData, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	return json.Unmarshal(respData, out)
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
	return c.PostLinesAs(ctx, channel, lines, "")
}

// PostLinesAs sends chat lines to a channel, naming speaker as the
// batch's speaker when one is given: the site files what the lines
// do not locate by the speaker's own position. With no speaker it
// sends the same body PostLines sends.
func (c *Client) PostLinesAs(ctx context.Context, channel int64, lines []string, speaker string) (PostResult, error) {
	body := map[string]any{"lines": lines}
	if speaker != "" {
		body["speaker"] = speaker
	}
	var out PostResult
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/intel/v1/channels/%d/lines", channel), body, &out)
	return out, err
}
