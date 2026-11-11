package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.4.5", "v0.4.4", true},
		{"v0.4.4", "v0.4.4", false},
		{"v0.4.3", "v0.4.4", false},
		{"v0.4.10", "v0.4.9", true}, // numeric, not lexical
		{"0.5.0", "v0.4.9", true},   // the leading v is optional
		{"v1.0.0", "v0.9.9", true},
		{"v0.4.5", "dev", false}, // an unnamed build never updates
		{"dev", "v0.4.4", false},
		{"v0.4.5-beta", "v0.4.4", false}, // suffixes are not plain versions
		{"v0.4", "v0.4.0", false},
		{"v0.4.1", "v0.4", true},
		{"", "v0.4.4", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"windows", "amd64"}: "axon-windows-amd64.exe",
		{"linux", "amd64"}:   "axon-linux-amd64",
		{"darwin", "arm64"}:  "axon-darwin-arm64",
		{"darwin", "amd64"}:  "axon-darwin-amd64",
	}
	for in, want := range cases {
		if got := AssetName(in[0], in[1]); got != want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestParseSums(t *testing.T) {
	prog := sha256.Sum256([]byte("the program"))
	other := sha256.Sum256([]byte("another file"))
	data := fmt.Sprintf("%x  axon-linux-amd64\n%x *axon-windows-amd64.exe\n",
		prog, other)
	sums, err := ParseSums([]byte(data))
	if err != nil {
		t.Fatalf("ParseSums: %v", err)
	}
	if sums["axon-linux-amd64"] != prog {
		t.Errorf("linux sum = %x, want %x", sums["axon-linux-amd64"], prog)
	}
	if sums["axon-windows-amd64.exe"] != other {
		t.Errorf("windows sum = %x, want %x (the * marker should be read past)", sums["axon-windows-amd64.exe"], other)
	}
	if _, err := ParseSums([]byte("nothex  axon-linux-amd64\n")); err == nil {
		t.Error("ParseSums accepted a non-hex sum")
	}
	if _, err := ParseSums([]byte("\n")); err == nil {
		t.Error("ParseSums accepted an empty file")
	}
	if _, err := ParseSums([]byte("abc123\n")); err == nil {
		t.Error("ParseSums accepted a line with no file name")
	}
}

// a release server: the newest-release JSON and the files it
// names, all on one test server. The sums are signed by priv; the
// checker under test trusts only the key it is given.
type releaseServer struct {
	*httptest.Server
	prog []byte
}

func newReleaseServer(t *testing.T, priv ed25519.PrivateKey, tag string, prog []byte, sumsOverride []byte) *releaseServer {
	t.Helper()
	rs := &releaseServer{prog: prog}
	sum := sha256.Sum256(prog)
	sums := fmt.Sprintf("%x  axon-linux-amd64\n", sum)
	if sumsOverride != nil {
		sums = string(sumsOverride)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums))) + "\n"
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"assets": []map[string]string{
				{"name": "SHA256SUMS", "browser_download_url": base + "/SHA256SUMS"},
				{"name": "SHA256SUMS.sig", "browser_download_url": base + "/SHA256SUMS.sig"},
				{"name": "axon-linux-amd64", "browser_download_url": base + "/axon-linux-amd64"},
			},
		})
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sums)) })
	mux.HandleFunc("/SHA256SUMS.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sig)) })
	mux.HandleFunc("/axon-linux-amd64", func(w http.ResponseWriter, r *http.Request) { w.Write(prog) })
	rs.Server = httptest.NewServer(mux)
	base = rs.URL
	t.Cleanup(rs.Close)
	return rs
}

func testChecker(pub ed25519.PublicKey, rs *releaseServer) *Checker {
	return &Checker{
		Keys:   []ed25519.PublicKey{pub},
		Latest: rs.URL + "/latest",
		GOOS:   "linux", GOARCH: "amd64",
	}
}

func TestCheckAndDownload(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	prog := []byte("the new axon, pretend binary bytes")
	rs := newReleaseServer(t, priv, "v0.4.5", prog, nil)
	c := testChecker(pub, rs)

	rel, err := c.Check(context.Background(), "v0.4.4")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel == nil {
		t.Fatal("Check named no release for v0.4.4 with v0.4.5 out")
	}
	if rel.Version != "v0.4.5" || rel.Asset != "axon-linux-amd64" {
		t.Fatalf("release = %+v, want version v0.4.5 and the linux asset", rel)
	}

	dir := t.TempDir()
	path, err := c.Download(context.Background(), rel, dir)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(prog) {
		t.Errorf("downloaded %q, want %q", got, prog)
	}
	if filepath.Base(path) != "axon-linux-amd64" {
		t.Errorf("downloaded to %q, want it named as the asset", path)
	}
}

func TestCheckNotNewer(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	rs := newReleaseServer(t, priv, "v0.4.5", []byte("x"), nil)
	c := testChecker(pub, rs)
	for _, current := range []string{"v0.4.5", "v0.5.0", "dev"} {
		rel, err := c.Check(context.Background(), current)
		if err != nil {
			t.Fatalf("Check(%q): %v", current, err)
		}
		if rel != nil {
			t.Errorf("Check(%q) named release %s, want none", current, rel.Version)
		}
	}
}

func TestDownloadRejectsUntrustedSignature(t *testing.T) {
	trustedPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	rs := newReleaseServer(t, otherPriv, "v0.4.5", []byte("the program"), nil)
	c := testChecker(trustedPub, rs)
	rel, err := c.Check(context.Background(), "v0.4.4")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), rel, dir); err == nil {
		t.Fatal("Download accepted sums signed by an untrusted key")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused download left %d files behind", len(entries))
	}
}

func TestDownloadRejectsDigestMismatch(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	// The sums (validly signed) name the sum of other bytes than
	// the program the server hands out.
	wrong := sha256.Sum256([]byte("not the program"))
	sums := fmt.Sprintf("%s  axon-linux-amd64\n", hex.EncodeToString(wrong[:]))
	rs := newReleaseServer(t, priv, "v0.4.5", []byte("the program"), []byte(sums))
	c := testChecker(pub, rs)
	rel, err := c.Check(context.Background(), "v0.4.4")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), rel, dir); err == nil {
		t.Fatal("Download accepted a program whose sum does not match")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused download left %d files behind", len(entries))
	}
}

func TestCheckOfflineFailsQuiet(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing listens on port 1: the fetch is refused at once, as
	// it is with no network at all.
	c := &Checker{Keys: []ed25519.PublicKey{pub}, Latest: "http://127.0.0.1:1/latest", GOOS: "linux", GOARCH: "amd64"}
	if _, err := c.Check(context.Background(), "v0.4.4"); err == nil {
		t.Fatal("Check against a dead server succeeded, want an error the caller can ignore")
	}
}
