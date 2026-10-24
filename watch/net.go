package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// The site API client has to survive the owner's VPN, whose DNS
// intermittently answers "no such host" for the site's name while
// everything else about the connection is fine. Two layers here:
//
//   - The transport dials as usual, but when the system resolver
//     fails it resolves the host through DNS-over-HTTPS instead and
//     dials the address it got. TLS still validates against the
//     original hostname (the transport takes the server name from
//     the URL, not the dialed address), so certificates stay strict.
//   - Client.do retries transient transport failures (client.go).
//
// Nothing here is tied to one site: whatever host the configured
// server address names is what gets resolved.

// siteHTTPClient is the HTTP client for calls to the site's API.
func siteHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, Transport: siteTransport}
}

var siteTransport = newSiteTransport()

func newSiteTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	d := &fallbackDialer{
		base:    dialFunc((&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext),
		resolve: dohResolve,
	}
	tr.DialContext = d.DialContext
	return tr
}

// dialFunc adapts a dial function to the one method fallbackDialer
// needs, so tests can stand in for the system dialer.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func (f dialFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
}

type dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// fallbackDialer dials through base, and when base reports a DNS
// failure it resolves the host via resolve (DNS-over-HTTPS in
// production) and dials the resulting addresses directly. Any other
// failure, and any failure of the fallback itself, surfaces the
// original error.
type fallbackDialer struct {
	base    dialer
	resolve func(ctx context.Context, host string) ([]string, error)
}

func (d *fallbackDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.base.DialContext(ctx, network, addr)
	if err == nil {
		return conn, nil
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		return nil, err
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil || net.ParseIP(host) != nil {
		return nil, err
	}
	ips, rerr := d.resolve(ctx, host)
	if rerr != nil || len(ips) == 0 {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		conn, derr := d.base.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, err
}

// --- DNS-over-HTTPS fallback resolution ---

// A DoH provider: its JSON API endpoint, the hostname the endpoint
// is served for (TLS validates against it), and the provider's own
// addresses, dialed directly so the fallback never depends on the
// system resolver that just failed.
type dohProvider struct {
	endpoint string
	host     string
	ips      []string
}

var dohProviders = []dohProvider{
	{"https://cloudflare-dns.com/dns-query", "cloudflare-dns.com", []string{"1.1.1.1", "1.0.0.1"}},
	{"https://dns.google/resolve", "dns.google", []string{"8.8.8.8", "8.8.4.4"}},
}

// dohClient asks the providers. Its transport dials the providers'
// fixed addresses; the TLS server name still comes from the URL, so
// the providers' certificates validate as usual.
var dohClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 3 * time.Second}
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return d.DialContext(ctx, network, addr)
			}
			for _, p := range dohProviders {
				if host != p.host {
					continue
				}
				var lastErr error
				for _, ip := range p.ips {
					conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
					if derr == nil {
						return conn, nil
					}
					lastErr = derr
				}
				return nil, lastErr
			}
			return d.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout: 3 * time.Second,
	},
}

type dohEntry struct {
	ips     []string
	expires time.Time
}

var dohCache = struct {
	sync.Mutex
	m map[string]dohEntry
}{m: map[string]dohEntry{}}

// dohResolve answers host's addresses from cache or DoH.
func dohResolve(ctx context.Context, host string) ([]string, error) {
	dohCache.Lock()
	if e, ok := dohCache.m[host]; ok && time.Now().Before(e.expires) {
		dohCache.Unlock()
		return e.ips, nil
	}
	dohCache.Unlock()

	ips, ttl, err := dohQuery(ctx, host)
	if err != nil {
		return nil, err
	}
	dohCache.Lock()
	dohCache.m[host] = dohEntry{ips: ips, expires: time.Now().Add(ttl)}
	dohCache.Unlock()
	return ips, nil
}

// dohQuery asks each provider in turn for host's A and AAAA records
// and returns the addresses and a cache lifetime from the records'
// TTLs (bounded, so a changed address is picked up reasonably soon
// and a fresh one is not re-asked on every dial).
func dohQuery(ctx context.Context, host string) ([]string, time.Duration, error) {
	var lastErr error
	for _, p := range dohProviders {
		ips, ttl, err := dohQueryProvider(ctx, p, host)
		if err == nil && len(ips) > 0 {
			return ips, ttl, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no DoH provider has an address for %s", host)
	}
	return nil, 0, lastErr
}

func dohQueryProvider(ctx context.Context, p dohProvider, host string) ([]string, time.Duration, error) {
	var ips []string
	ttl := 60 * time.Second
	seenTTL := false
	for _, qtype := range []string{"A", "AAAA"} {
		u := p.endpoint + "?" + url.Values{"name": {host}, "type": {qtype}}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Accept", "application/dns-json")
		resp, err := dohClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		var body struct {
			Answer []struct {
				Type int    `json:"type"`
				TTL  int    `json:"TTL"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, 0, fmt.Errorf("DoH provider %s said %s", p.host, resp.Status)
		}
		for _, a := range body.Answer {
			if (a.Type == 1 || a.Type == 28) && net.ParseIP(a.Data) != nil {
				ips = append(ips, a.Data)
				if a.TTL > 0 && (!seenTTL || time.Duration(a.TTL)*time.Second < ttl) {
					ttl = time.Duration(a.TTL) * time.Second
					seenTTL = true
				}
			}
		}
	}
	if ttl < 30*time.Second {
		ttl = 30 * time.Second
	}
	if ttl > 5*time.Minute {
		ttl = 5 * time.Minute
	}
	return ips, ttl, nil
}
