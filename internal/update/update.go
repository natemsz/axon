// Package update checks whether a newer axon release exists and
// fetches it, trusting nothing until it is proven: the release's
// SHA256SUMS must carry a signature that verifies against the
// release key the repo trusts (internal/releasesig, the keys from
// release-signing.pub), and the downloaded program must hash to
// the sum that file lists for it. A check or a fetch that fails
// for any reason (offline, a bad signature, a digest that does
// not match) is just an error to the caller, which carries on
// with the axon it has.
package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/natemsz/axon/internal/releasesig"
)

// LatestURL is where the newest published release is named: the
// release workflow publishes to the GitHub mirror.
const LatestURL = "https://api.github.com/repos/natemsz/axon/releases/latest"

const (
	maxSums = 1 << 20   // SHA256SUMS is a few hundred bytes
	maxSig  = 64 << 10  // a few base64 lines
	maxProg = 512 << 20 // far above any axon build
)

// Release is a newer release and where its files are.
type Release struct {
	Version string // the release tag, e.g. "v0.4.5"
	Asset   string // this platform's program file name

	sumsURL, sigURL, assetURL string
}

// Checker checks for and fetches releases, verifying signatures
// against Keys.
type Checker struct {
	Keys []ed25519.PublicKey
	// HTTP does the fetching; nil means http.DefaultClient.
	HTTP *http.Client
	// Latest is the newest-release endpoint; "" means LatestURL.
	Latest string
	// GOOS and GOARCH name the platform asset; "" means this
	// program's own platform.
	GOOS, GOARCH string
}

func (c *Checker) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Checker) latest() string {
	if c.Latest != "" {
		return c.Latest
	}
	return LatestURL
}

// AssetName is the release file for a platform, as the release
// workflow names it.
func AssetName(goos, goarch string) string {
	name := "axon-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Newer reports whether version a is newer than version b. Both
// are dotted numbers with an optional leading "v" ("v0.4.5",
// "0.4.4"); anything else (a dev build, a suffix) is never newer,
// so a build that cannot name its version does not update.
func Newer(a, b string) bool {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(s, "v")
	parts := strings.Split(s, ".")
	if len(parts) > len(v) {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// ParseSums reads a SHA256SUMS file: one "<hex sha256>  <name>"
// line per file, as sha256sum writes it (a name may carry the "*"
// binary marker).
func ParseSums(data []byte) (map[string][sha256.Size]byte, error) {
	sums := make(map[string][sha256.Size]byte)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("SHA256SUMS: bad line %q", line)
		}
		raw, err := hex.DecodeString(fields[0])
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("SHA256SUMS: bad sum in line %q", line)
		}
		var sum [sha256.Size]byte
		copy(sum[:], raw)
		sums[strings.TrimPrefix(fields[1], "*")] = sum
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("SHA256SUMS: no sums in it")
	}
	return sums, nil
}

// Check names the newest release when it is newer than current,
// and returns nil (and no error) when it is not. The release is
// only named, not fetched: Download does the fetching, and the
// proving, when the player asks for the update.
func (c *Checker) Check(ctx context.Context, current string) (*Release, error) {
	body, err := c.get(ctx, c.latest(), maxSums)
	if err != nil {
		return nil, err
	}
	var latest struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &latest); err != nil {
		return nil, fmt.Errorf("reading the newest release: %w", err)
	}
	if !Newer(latest.Tag, current) {
		return nil, nil
	}
	goos, goarch := c.GOOS, c.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	rel := &Release{Version: latest.Tag, Asset: AssetName(goos, goarch)}
	for _, a := range latest.Assets {
		switch a.Name {
		case "SHA256SUMS":
			rel.sumsURL = a.URL
		case "SHA256SUMS.sig":
			rel.sigURL = a.URL
		case rel.Asset:
			rel.assetURL = a.URL
		}
	}
	if rel.sumsURL == "" || rel.sigURL == "" || rel.assetURL == "" {
		return nil, fmt.Errorf("release %s does not carry everything an update needs", latest.Tag)
	}
	return rel, nil
}

// Download fetches the release's program into dir and returns its
// path, ready to run (and so to install itself, the way a manual
// download does). The sums file's signature is verified first and
// the program's own sum second; nothing is left in dir on failure.
func (c *Checker) Download(ctx context.Context, rel *Release, dir string) (string, error) {
	sums, err := c.get(ctx, rel.sumsURL, maxSums)
	if err != nil {
		return "", err
	}
	sig, err := c.get(ctx, rel.sigURL, maxSig)
	if err != nil {
		return "", err
	}
	if !releasesig.Verify(c.Keys, sums, sig) {
		return "", fmt.Errorf("the signature on release %s does not verify; refusing the update", rel.Version)
	}
	parsed, err := ParseSums(sums)
	if err != nil {
		return "", err
	}
	want, ok := parsed[rel.Asset]
	if !ok {
		return "", fmt.Errorf("SHA256SUMS for release %s does not list %s", rel.Version, rel.Asset)
	}

	path := filepath.Join(dir, rel.Asset)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	err = c.getTo(ctx, rel.assetURL, io.MultiWriter(f, h), maxProg)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	if got := h.Sum(nil); subtle.ConstantTimeCompare(got, want[:]) != 1 {
		os.Remove(path)
		return "", fmt.Errorf("the downloaded %s does not match its sum in SHA256SUMS; refusing the update", rel.Asset)
	}
	return path, nil
}

// getTo fetches url into w, refusing more than max bytes.
func (c *Checker) getTo(ctx context.Context, url string, w io.Writer, max int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "axon-update")
	res, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: %s", url, res.Status)
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, max+1))
	if err != nil {
		return err
	}
	if n > max {
		return fmt.Errorf("fetching %s: larger than %d bytes", url, max)
	}
	return nil
}

func (c *Checker) get(ctx context.Context, url string, max int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := c.getTo(ctx, url, &buf, max); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
