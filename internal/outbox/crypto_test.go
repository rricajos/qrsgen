package outbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func newTestKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, EncryptionKeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestDecodeEncryptionKey_Valid(t *testing.T) {
	raw := newTestKey(t)
	b64 := base64.StdEncoding.EncodeToString(raw)
	out, err := DecodeEncryptionKey(b64)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !bytes.Equal(raw, out) {
		t.Error("round-trip mismatch")
	}
}

func TestDecodeEncryptionKey_Empty(t *testing.T) {
	out, err := DecodeEncryptionKey("")
	if err != nil || out != nil {
		t.Errorf("empty input should yield nil, nil; got %v, %v", out, err)
	}
}

func TestDecodeEncryptionKey_URLSafe(t *testing.T) {
	// Generar key con bytes que produzcan diferentes encodings entre std/url.
	raw := []byte{
		0xfb, 0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 0xf9,
		0xf8, 0xf7, 0xf6, 0xf5, 0xf4, 0xf3, 0xf2, 0xf1,
		0xf0, 0xef, 0xee, 0xed, 0xec, 0xeb, 0xea, 0xe9,
		0xe8, 0xe7, 0xe6, 0xe5, 0xe4, 0xe3, 0xe2, 0xe1,
	}
	urlSafe := base64.URLEncoding.EncodeToString(raw)
	out, err := DecodeEncryptionKey(urlSafe)
	if err != nil {
		t.Fatalf("URL-safe should decode: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Errorf("URL-safe round-trip mismatch")
	}
	// Sin padding
	rawURL := base64.RawURLEncoding.EncodeToString(raw)
	out, err = DecodeEncryptionKey(rawURL)
	if err != nil {
		t.Fatalf("RawURL should decode: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Errorf("RawURL round-trip mismatch")
	}
}

func TestDecodeEncryptionKey_WrongSize(t *testing.T) {
	// 16 bytes is wrong (AES-256 expects 32).
	bad := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0}, 16))
	if _, err := DecodeEncryptionKey(bad); err == nil {
		t.Error("expected size error")
	}
}

func TestSealOpen_RoundTrip(t *testing.T) {
	key := newTestKey(t)
	plain := []byte(`{"event":"message_created","content":"hola"}`)
	ct, nonce, err := sealPayload(key, plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Equal(ct, plain) {
		t.Error("ciphertext should differ from plaintext")
	}
	pt, err := openPayload(key, ct, nonce)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(pt, plain) {
		t.Errorf("plaintext mismatch: got %s", pt)
	}
}

func TestOpen_BackwardCompat_NoNonce(t *testing.T) {
	// Filas pre-encryption: nonce vacío → payload tratado como plaintext.
	pt := []byte(`legacy plaintext`)
	out, err := openPayload(nil, pt, nil)
	if err != nil || !bytes.Equal(out, pt) {
		t.Errorf("legacy passthrough failed: %v, %s", err, out)
	}
}

func TestOpen_KeyMismatch(t *testing.T) {
	k1 := newTestKey(t)
	k2 := newTestKey(t)
	ct, nonce, err := sealPayload(k1, []byte("x"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := openPayload(k2, ct, nonce); err == nil {
		t.Error("expected open to fail with wrong key")
	}
}
