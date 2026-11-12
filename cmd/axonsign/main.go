// axonsign signs axon release artifacts with the release key,
// mirroring the EveSynapse scheme (internal/releasesig there):
// the private half is an Ed25519 key in the RELEASE_SIGNING_KEY
// environment variable (a repo Actions secret), held either as the
// PEM itself or as the PEM base64-encoded whole. A signature is the
// base64 of the raw Ed25519 signature over the file's bytes written
// to <file>.sig, and signing refuses to proceed unless the key's
// public half is listed in the trusted file (release-signing.pub
// at the repo root), so a release can only ever be signed by a key
// this repo has declared trusted.
//
// Usage:
//
//	axonsign sign <file>...    sign each file, writing <file>.sig
//	axonsign verify <file>...  check each <file>.sig against the trusted keys
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"github.com/natemsz/axon/internal/releasesig"
)

const trustedPath = "release-signing.pub"

// parsePrivate reads the signing key(s) in RELEASE_SIGNING_KEY:
// PEM "PRIVATE KEY" blocks (PKCS #8) holding Ed25519 keys. The
// value may instead be the PEM base64-encoded, whole: a secret
// store that masks single-line values only keeps the key there
// encoded, and both forms are read here.
func parsePrivate(pemData []byte) ([]ed25519.PrivateKey, error) {
	if !strings.Contains(string(pemData), "-----BEGIN ") {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pemData))); err == nil {
			pemData = decoded
		}
	}
	var keys []ed25519.PrivateKey
	for {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			break
		}
		if block.Type == "PUBLIC KEY" {
			return nil, fmt.Errorf("RELEASE_SIGNING_KEY holds a PUBLIC key (-----BEGIN PUBLIC KEY-----); the secret must hold the private half, the file whose first line is -----BEGIN PRIVATE KEY-----")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("RELEASE_SIGNING_KEY is not a parseable PKCS#8 private key (want the complete PEM, -----BEGIN PRIVATE KEY----- through -----END PRIVATE KEY-----, nothing trimmed): %w", err)
		}
		priv, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("signing key is not Ed25519")
		}
		keys = append(keys, priv)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no private key in RELEASE_SIGNING_KEY")
	}
	return keys, nil
}

func trusted() ([]ed25519.PublicKey, error) {
	raw, err := os.ReadFile(trustedPath)
	if err != nil {
		return nil, err
	}
	return releasesig.ParsePublicKeys(raw)
}

func sign(files []string) error {
	secret := os.Getenv("RELEASE_SIGNING_KEY")
	if secret == "" {
		return fmt.Errorf("RELEASE_SIGNING_KEY is not set; refusing to sign")
	}
	privs, err := parsePrivate([]byte(secret))
	if err != nil {
		return err
	}
	trust, err := trusted()
	if err != nil {
		return err
	}
	for _, priv := range privs {
		pub := priv.Public().(ed25519.PublicKey)
		found := false
		for _, t := range trust {
			if pub.Equal(t) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("the signing key is not one this repo trusts; its public half is not in %s. Nothing was signed", trustedPath)
		}
	}
	for _, f := range files {
		msg, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var lines []string
		for _, priv := range privs {
			lines = append(lines, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)))
		}
		if err := os.WriteFile(f+".sig", []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("signed %s\n", f)
	}
	return nil
}

func verify(files []string) error {
	trust, err := trusted()
	if err != nil {
		return err
	}
	for _, f := range files {
		msg, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		sigFile, err := os.ReadFile(f + ".sig")
		if err != nil {
			return err
		}
		if !releasesig.Verify(trust, msg, sigFile) {
			return fmt.Errorf("%s: no signature verifies against %s", f, trustedPath)
		}
		fmt.Printf("verified %s\n", f)
	}
	return nil
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: axonsign sign|verify <file>...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "usage: axonsign sign|verify <file>...")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "axonsign:", err)
		os.Exit(1)
	}
}
