package releasesig

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

// keypair returns a fresh Ed25519 key and its public half as the
// PKIX PEM release-signing.pub lists.
func keypair(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestParsePublicKeysBehindProse(t *testing.T) {
	_, pubPEM := keypair(t)
	keys, err := ParsePublicKeys([]byte("The public half of the key.\n\n" + pubPEM))
	if err != nil {
		t.Fatalf("ParsePublicKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
}

func TestParsePublicKeysNone(t *testing.T) {
	if _, err := ParsePublicKeys([]byte("no keys here")); err == nil {
		t.Fatal("ParsePublicKeys of keyless data succeeded, want an error")
	}
}

func TestVerifyAcceptsAndRejects(t *testing.T) {
	priv, pubPEM := keypair(t)
	otherPriv, _ := keypair(t)
	keys, err := ParsePublicKeys([]byte(pubPEM))
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("abc123  axon-linux-amd64\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)) + "\n"
	if !Verify(keys, msg, []byte(sig)) {
		t.Fatal("Verify rejected a good signature")
	}
	if Verify(keys, []byte("def456  axon-linux-amd64\n"), []byte(sig)) {
		t.Fatal("Verify accepted a signature over different bytes")
	}
	bad := base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, msg)) + "\n"
	if Verify(keys, msg, []byte(bad)) {
		t.Fatal("Verify accepted a signature from an untrusted key")
	}
	if Verify(keys, msg, []byte("not base64 at all\n")) {
		t.Fatal("Verify accepted a signature file with no signature in it")
	}
}
