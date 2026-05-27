package outbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// EncryptionKeySize es el tamaño esperado de la key AES-GCM en bytes.
const EncryptionKeySize = 32 // AES-256

// DecodeEncryptionKey decodifica una key base64. Acepta tanto base64 estándar
// (`+/=`) como URL-safe (`-_=`), con o sin padding — habitual cuando la key
// viene de un secret manager (Vault, GitHub Actions, etc) que normaliza a
// URL-safe. Verifica longitud final = EncryptionKeySize.
//
// Devuelve nil + nil error cuando el input es vacío (sin cifrado opt-out).
func DecodeEncryptionKey(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, nil
	}
	// Probar 4 variantes: {std, url} × {padded, raw}.
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		raw, err := enc.DecodeString(b64)
		if err == nil {
			if len(raw) != EncryptionKeySize {
				return nil, fmt.Errorf("outbox key: want %d bytes after decode, got %d", EncryptionKeySize, len(raw))
			}
			return raw, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("outbox key: base64 decode (probados std/url, padded/raw): %w", lastErr)
}

// sealPayload cifra `plaintext` con AES-256-GCM. Devuelve ciphertext + nonce
// (12 bytes random). El caller persiste ambos en la fila del outbox.
func sealPayload(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	if len(key) != EncryptionKeySize {
		return nil, nil, errors.New("outbox seal: bad key size")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("outbox seal: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("outbox seal: gcm: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("outbox seal: nonce read: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// openPayload descifra ciphertext con la key y nonce dados. Si el nonce está
// vacío, se asume que el ciphertext es realmente plaintext en claro (filas
// pre-encryption, backward compat).
func openPayload(key, ciphertext, nonce []byte) ([]byte, error) {
	if len(nonce) == 0 {
		return ciphertext, nil
	}
	if len(key) != EncryptionKeySize {
		return nil, errors.New("outbox open: key missing or wrong size for encrypted row")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("outbox open: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("outbox open: gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("outbox open: %w", err)
	}
	return plaintext, nil
}
