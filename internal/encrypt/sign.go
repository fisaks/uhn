package encrypt

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"fmt"
)

// Sign signs data with the edge's Ed25519 private key.
func (kp *EdgeKeyPair) Sign(data []byte) []byte {
	return ed25519.Sign(kp.PrivateKey, data)
}

// SignBase64 signs data and returns the signature as a base64 string.
func (kp *EdgeKeyPair) SignBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(kp.Sign(data))
}

// ParseSPKIPublicKey decodes a base64-encoded SPKI DER public key to a raw ed25519.PublicKey.
func ParseSPKIPublicKey(base64SPKI string) (ed25519.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(base64SPKI)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse SPKI: %w", err)
	}

	edPub, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 public key, got %T", pub)
	}

	return edPub, nil
}

// VerifySignature verifies an Ed25519 signature over data using the given public key.
func VerifySignature(publicKey ed25519.PublicKey, data, signature []byte) bool {
	return ed25519.Verify(publicKey, data, signature)
}
