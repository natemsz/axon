package watch

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is Axon's half of device pairing (schema 081 on the
// site). Axon holds an Ed25519 keypair per config: the private key
// in axon.key beside the config (0600, never sent anywhere), the
// public key handed to the site once, while pairing. Every API
// request is then signed, so the token in axon.json only works
// from this computer.

// KeyPath is the private key file belonging to a config file.
func KeyPath(configPath string) string {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	return filepath.Join(filepath.Dir(abs), "axon.key")
}

// LoadOrCreateKey reads the private key at path, making a new one
// (0600) when there is none yet.
func LoadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%s is not an Axon key: delete it and pair again", path)
		}
		return ed25519.PrivateKey(raw), nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	text := base64.StdEncoding.EncodeToString(priv) + "\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

// LoadKey reads an existing private key; a missing one is an error
// naming pairing as the fix.
func LoadKey(path string) (ed25519.PrivateKey, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("this Axon is not paired yet (no %s): open the settings page and pair it", filepath.Base(path))
	}
	return LoadOrCreateKey(path)
}

// PublicKeyText is the public half, as the site stores it.
func PublicKeyText(priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}

// SignText is what gets signed, in both repos (the site's
// axonSignatureText): the method, the path, the timestamp and the
// body's hash, so a captured request cannot be replayed later or
// pointed elsewhere.
func SignText(method, path, timestamp string, body []byte) string {
	sum := sha256.Sum256(body)
	return method + "\n" + path + "\n" + timestamp + "\n" + hex.EncodeToString(sum[:])
}

// Pair trades a one-time pairing code from the site for the device
// credential, returning the token and the account's name. The site
// binds the token to priv's public key.
func Pair(ctx context.Context, server, code, device string, priv ed25519.PrivateKey) (token, account string, err error) {
	if err := CheckServer(server); err != nil {
		return "", "", err
	}
	body, _ := json.Marshal(map[string]string{
		"code": code, "public_key": PublicKeyText(priv), "device": device,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(server, "/")+"/api/intel/v1/pair", strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var out struct {
		Token   string `json:"token"`
		Account string `json:"account"`
		Error   string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return "", "", &APIError{Status: resp.StatusCode, Message: out.Error}
	}
	if out.Token == "" {
		return "", "", fmt.Errorf("the site paired nothing: try a fresh code")
	}
	return out.Token, out.Account, nil
}
