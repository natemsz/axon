// Package releasesig is the release signature scheme axon and
// axonsign share: a signature is the base64 of the raw Ed25519
// signature over a file's bytes, one per line, and it counts when
// it verifies under any of the public keys the repo trusts (the
// PEM keys in release-signing.pub at the repo root, which may sit
// behind prose). Signing lives in cmd/axonsign; checking a
// signature is needed both there and by the self-update in
// internal/update, so the checking half lives here.
package releasesig

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ParsePublicKeys reads the trusted public keys out of PEM data:
// every "PUBLIC KEY" block holding an Ed25519 key. Prose around
// the blocks is ignored.
func ParsePublicKeys(pemData []byte) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			break
		}
		if block.Type != "PUBLIC KEY" {
			continue
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		pub, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("trusted key is not Ed25519")
		}
		keys = append(keys, pub)
	}
	if len(keys) == 0 {
		return nil, errors.New("no public keys in the trusted data")
	}
	return keys, nil
}

// Verify reports whether any signature in sigData (base64, one per
// line, as a .sig file holds) verifies msg under any of keys.
func Verify(keys []ed25519.PublicKey, msg, sigData []byte) bool {
	for _, line := range strings.Split(strings.TrimSpace(string(sigData)), "\n") {
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		for _, pub := range keys {
			if ed25519.Verify(pub, msg, sig) {
				return true
			}
		}
	}
	return false
}
