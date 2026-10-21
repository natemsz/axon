package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// keypair returns a fresh Ed25519 key and its PEM encodings, the
// private half as the PKCS#8 PEM the RELEASE_SIGNING_KEY secret
// holds and the public half as the PKIX PEM release-signing.pub
// lists.
func keypair(t *testing.T) (ed25519.PrivateKey, string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return priv, string(privPEM), string(pubPEM)
}

// setup moves the test into a scratch directory holding a trusted
// file that trusts pubPEM (behind a prose header, as the committed
// release-signing.pub has) and one sums file, and returns the sums
// path.
func setup(t *testing.T, pubPEM string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	trusted := "The public half of the key axon releases are signed with.\n\n" + pubPEM
	if err := os.WriteFile(trustedPath, []byte(trusted), 0o644); err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, []byte("abc123  axon-linux-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return sums
}

func TestSignVerifyRoundTrip(t *testing.T) {
	_, privPEM, pubPEM := keypair(t)
	sums := setup(t, pubPEM)
	t.Setenv("RELEASE_SIGNING_KEY", privPEM)
	if err := sign([]string{sums}); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := os.Stat(sums + ".sig"); err != nil {
		t.Fatalf("signature file: %v", err)
	}
	if err := verify([]string{sums}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSignRefusesUntrustedKey(t *testing.T) {
	_, _, trustedPubPEM := keypair(t)
	_, otherPrivPEM, _ := keypair(t)
	sums := setup(t, trustedPubPEM)
	t.Setenv("RELEASE_SIGNING_KEY", otherPrivPEM)
	if err := sign([]string{sums}); err == nil {
		t.Fatal("sign with an untrusted key succeeded, want refusal")
	}
	if _, err := os.Stat(sums + ".sig"); !os.IsNotExist(err) {
		t.Fatalf("signature file exists after refused sign: %v", err)
	}
}

func TestVerifyFailsOnTamperedSums(t *testing.T) {
	_, privPEM, pubPEM := keypair(t)
	sums := setup(t, pubPEM)
	t.Setenv("RELEASE_SIGNING_KEY", privPEM)
	if err := sign([]string{sums}); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := os.WriteFile(sums, []byte("def456  axon-linux-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verify([]string{sums}); err == nil {
		t.Fatal("verify of tampered sums succeeded, want failure")
	}
}

func TestSignRejectsPublicKeyAsSecret(t *testing.T) {
	_, _, pubPEM := keypair(t)
	setup(t, pubPEM)
	t.Setenv("RELEASE_SIGNING_KEY", pubPEM)
	if err := sign([]string{"SHA256SUMS"}); err == nil {
		t.Fatal("sign with a public key as the secret succeeded, want refusal")
	}
}
