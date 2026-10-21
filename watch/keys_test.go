package watch

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCheckServer(t *testing.T) {
	for server, ok := range map[string]bool{
		"https://evesynapse.example":  true,
		"https://evesynapse.example/": true,
		"http://127.0.0.1:8080":       true,
		"http://localhost:8080":       true,
		"http://dev.localhost:8080":   true,
		"http://evesynapse.example":   false,
		"http://192.168.1.20:8080":    false,
		"evesynapse.example":          false,
		"https://":                    false,
		"":                            false,
	} {
		if err := CheckServer(server); (err == nil) != ok {
			t.Errorf("CheckServer(%q) err=%v, want ok=%v", server, err, ok)
		}
		if err := (Config{Server: server, Token: "esw_x"}).Check(); (err == nil) != ok {
			// Config.Check may still fail a bare https:// for a
			// different reason; only the direction matters here.
			if ok && server != "https://" {
				t.Errorf("Config.Check(%q) err=%v", server, err)
			}
		}
	}
}

func TestKeyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "axon.key")
	priv, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st != nil && st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("key file readable by others: %v", st.Mode())
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || !again.Equal(priv) {
		t.Errorf("key changed on reload: %v", err)
	}
	if _, err := LoadKey(filepath.Join(t.TempDir(), "missing.key")); err == nil {
		t.Error("a missing key loads without error")
	}
}

func TestPairAndSignedRequests(t *testing.T) {
	var pairedPub, reason string
	devToken := "esw_" + "dev"
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/intel/v1/pair":
			var in struct {
				Code      string `json:"code"`
				PublicKey string `json:"public_key"`
				Device    string `json:"device"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Code != "K7QX-4MZD" || in.Device != "Test PC" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"bad code"}`))
				return
			}
			pairedPub = in.PublicKey
			_, _ = w.Write([]byte(`{"ok":true,"token":"esw_dev","account":"Pilot"}`))
		case "/api/intel/v1/channels":
			if r.Header.Get("Authorization") != "Bearer "+devToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			pub, _ := base64.StdEncoding.DecodeString(pairedPub)
			ts := r.Header.Get("X-Axon-Timestamp")
			at, _ := strconv.ParseInt(ts, 10, 64)
			if time.Now().Unix()-at > 60 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-Axon-Signature"))
			if !ed25519.Verify(ed25519.PublicKey(pub), []byte(SignText(r.Method, r.URL.RequestURI(), ts, nil)), sig) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"bad signature"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"account":"Pilot","channels":[{"id":2,"name":"b","post":true}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer site.Close()

	priv, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "axon.key"))
	if err != nil {
		t.Fatal(err)
	}
	token, account, err := Pair(context.Background(), site.URL, "K7QX-4MZD", "Test PC", priv)
	if err != nil || token != "esw_dev" || account != "Pilot" {
		t.Fatalf("Pair: %q %q %v", token, account, err)
	}
	c := &Client{Server: site.URL, Token: token, Key: priv}
	if account, list, err := c.Channels(context.Background()); err != nil || account != "Pilot" || len(list) != 1 {
		t.Logf("mock reason=%q pairedPub=%q clientPub=%q", reason, pairedPub, PublicKeyText(priv))
		t.Errorf("signed Channels: %q %v %v", account, list, err)
	}
	// The same token signed by another key is refused.
	_, other, _ := ed25519.GenerateKey(nil)
	c.Key = other
	if _, _, err := c.Channels(context.Background()); err == nil || !strings.Contains(err.Error(), "bad signature") {
		t.Errorf("another key's signature: %v", err)
	}
	// Pairing against a plain-HTTP site off this computer is refused.
	if _, _, err := Pair(context.Background(), "http://evesynapse.example", "K7QX-4MZD", "Test PC", priv); err == nil {
		t.Error("Pair accepted a plain-HTTP remote site")
	}
}
