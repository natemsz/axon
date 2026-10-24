package watch

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedTransport plays back a script of errors and then a 200,
// recording each attempt's signing headers and body.
type scriptedTransport struct {
	mu     sync.Mutex
	errs   []error
	stamps []string
	sigs   []string
	bodies [][]byte
}

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	s.mu.Lock()
	s.stamps = append(s.stamps, req.Header.Get("X-Axon-Timestamp"))
	s.sigs = append(s.sigs, req.Header.Get("X-Axon-Signature"))
	s.bodies = append(s.bodies, body)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"posted":2,"duplicates":0,"skipped":0}`)),
	}, nil
}

func fastWaits(t *testing.T) {
	t.Helper()
	old := retryWaits
	retryWaits = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryWaits = old })
}

// A call that fails the way the owner's VPN fails it — the name
// does not resolve — is retried, and every attempt is signed fresh:
// the site treats each signature as single-use, so a replayed one
// would be refused.
func TestDoRetriesDNSFailureAndResigns(t *testing.T) {
	fastWaits(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dnsErr := &net.DNSError{IsNotFound: true, Name: "evesynapse.example"}
	tr := &scriptedTransport{errs: []error{dnsErr, dnsErr}}
	c := &Client{
		Server: "https://evesynapse.example",
		Token:  "esw_test",
		Key:    key,
		HTTP:   &http.Client{Transport: tr},
	}
	res, err := c.PostLines(context.Background(), 7, []string{"one hostile in Jita"})
	if err != nil {
		t.Fatalf("PostLines: %v", err)
	}
	if res.Posted != 2 {
		t.Fatalf("posted = %d, want 2", res.Posted)
	}
	if len(tr.stamps) != 3 {
		t.Fatalf("attempts = %d, want 3", len(tr.stamps))
	}
	path := "/api/intel/v1/channels/7/lines"
	for i := range tr.stamps {
		sig, err := base64.StdEncoding.DecodeString(tr.sigs[i])
		if err != nil {
			t.Fatalf("attempt %d: signature is not base64: %v", i, err)
		}
		text := SignText(http.MethodPost, path, tr.stamps[i], tr.bodies[i])
		if !ed25519.Verify(key.Public().(ed25519.PublicKey), []byte(text), sig) {
			t.Fatalf("attempt %d: signature does not verify against its own timestamp", i)
		}
	}
}

// A refusal from the server is not retried here: the watcher
// decides what a 5xx means for its queue.
func TestDoDoesNotRetryAPIError(t *testing.T) {
	fastWaits(t)
	tr := &scriptedTransport{}
	c := &Client{Server: "https://evesynapse.example", Token: "esw_test", HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		tr.mu.Lock()
		tr.stamps = append(tr.stamps, "x")
		tr.mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"down"}`)),
		}, nil
	})}}
	_, _, err := c.Channels(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 500 {
		t.Fatalf("err = %v, want a 500 APIError", err)
	}
	if len(tr.stamps) != 1 {
		t.Fatalf("attempts = %d, want 1", len(tr.stamps))
	}
}

// A certificate failure is not transient: one attempt, error out.
func TestDoDoesNotRetryCertificateFailure(t *testing.T) {
	fastWaits(t)
	certErr := &tls.CertificateVerificationError{
		Err: x509.UnknownAuthorityError{},
	}
	tr := &scriptedTransport{errs: []error{certErr, certErr, certErr}}
	c := &Client{Server: "https://evesynapse.example", Token: "esw_test", HTTP: &http.Client{Transport: tr}}
	_, _, err := c.Channels(context.Background())
	if err == nil {
		t.Fatal("Channels succeeded, want the certificate error")
	}
	if len(tr.stamps) != 1 {
		t.Fatalf("attempts = %d, want 1", len(tr.stamps))
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
