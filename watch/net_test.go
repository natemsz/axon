package watch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// When the system dialer reports a DNS failure, the fallback
// dialer resolves the host its own way and dials that address;
// the connection still lands where it should.
func TestFallbackDialerResolvesOnDNSFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	real := &net.Dialer{}
	base := dialFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) == nil {
			return nil, &net.DNSError{IsNotFound: true, Name: host}
		}
		return real.DialContext(ctx, network, addr)
	})
	var resolved string
	d := &fallbackDialer{
		base: base,
		resolve: func(ctx context.Context, host string) ([]string, error) {
			resolved = host
			return []string{"127.0.0.1"}, nil
		},
	}
	conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort("gone.example", port))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	conn.Close()
	if resolved != "gone.example" {
		t.Fatalf("resolver was asked for %q, want gone.example", resolved)
	}
}

// A failure that is not DNS is the dialer's own business: it comes
// back untouched and the resolver is never asked.
func TestFallbackDialerPassesOtherErrorsThrough(t *testing.T) {
	boom := errors.New("boom")
	asked := false
	d := &fallbackDialer{
		base: dialFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, boom
		}),
		resolve: func(ctx context.Context, host string) ([]string, error) {
			asked = true
			return []string{"127.0.0.1"}, nil
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "gone.example:443")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the original dial error", err)
	}
	if asked {
		t.Fatal("resolver was asked despite a non-DNS failure")
	}
}

// And when the fallback cannot resolve either, the original DNS
// error is what the caller sees — that is the failure to report.
func TestFallbackDialerSurfacesOriginalError(t *testing.T) {
	d := &fallbackDialer{
		base: dialFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			return nil, &net.DNSError{IsNotFound: true, Name: host}
		}),
		resolve: func(ctx context.Context, host string) ([]string, error) {
			return nil, errors.New("DoH is down too")
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "gone.example:443")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("err = %v, want the original DNS error", err)
	}
}

// dohServer serves canned dns-json answers and counts requests.
func dohServer(t *testing.T, queries *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		w.Header().Set("Content-Type", "application/dns-json")
		if r.URL.Query().Get("type") == "AAAA" {
			fmt.Fprint(w, `{"Answer":[{"type":28,"TTL":120,"data":"2001:db8::7"}]}`)
			return
		}
		// A CNAME rides along with the address record; only the
		// address may be dialed.
		fmt.Fprint(w, `{"Answer":[{"type":5,"TTL":120,"data":"alias.example"},{"type":1,"TTL":120,"data":"203.0.113.7"}]}`)
	}))
}

// The DoH query takes the address records out of the answer, in
// order, and the cache keeps a second lookup from asking again.
func TestDoHResolveAndCache(t *testing.T) {
	var queries atomic.Int32
	srv := dohServer(t, &queries)
	defer srv.Close()

	oldProviders := dohProviders
	dohProviders = []dohProvider{{endpoint: srv.URL, host: "test", ips: nil}}
	defer func() { dohProviders = oldProviders }()
	dohCache.Lock()
	dohCache.m = map[string]dohEntry{}
	dohCache.Unlock()
	defer func() {
		dohCache.Lock()
		dohCache.m = map[string]dohEntry{}
		dohCache.Unlock()
	}()

	ctx := context.Background()
	ips, err := dohResolve(ctx, "evesynapse.example")
	if err != nil {
		t.Fatalf("dohResolve: %v", err)
	}
	want := []string{"203.0.113.7", "2001:db8::7"}
	if len(ips) != len(want) || ips[0] != want[0] || ips[1] != want[1] {
		t.Fatalf("ips = %v, want %v", ips, want)
	}
	asked := queries.Load()
	if _, err := dohResolve(ctx, "evesynapse.example"); err != nil {
		t.Fatalf("dohResolve again: %v", err)
	}
	if queries.Load() != asked {
		t.Fatal("second lookup asked the provider again instead of using the cache")
	}
	// The records said 120s; the cache must not keep them forever.
	dohCache.Lock()
	entry := dohCache.m["evesynapse.example"]
	dohCache.Unlock()
	if until := time.Until(entry.expires); until <= 0 || until > 2*time.Minute {
		t.Fatalf("cache entry expires in %s, want within the records' TTL", until)
	}
}
