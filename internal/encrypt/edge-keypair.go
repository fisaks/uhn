package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fisaks/uhn/internal/messaging"
)

type EdgePublicKeyMessage struct {
	EdgeID    string `json:"edgeId"`
	PublicKey string `json:"publicKey"`
	Algorithm string `json:"algorithm"`
	Ts        int64  `json:"ts"`
}

type EdgeKeyPair struct {
	EdgeID     string
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

func NewEdgeKeyPair(edgeId, path string) (*EdgeKeyPair, error) {
	dir := filepath.Dir(path)
	priv, pub, err := ensureKeyPair(dir)
	if err != nil {
		return nil, err
	}
	return &EdgeKeyPair{
		EdgeID:     edgeId,
		PrivateKey: priv,
		PublicKey:  pub,
	}, nil
}

func ensureKeyPair(dir string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	privPath := filepath.Join(dir, "edge.key")
	pubPath := filepath.Join(dir, "edge.pub")

	if _, err := os.Stat(privPath); err == nil {
		// load existing
		privBytes, err := os.ReadFile(privPath)
		if err != nil {
			return nil, nil, err
		}
		priv, err := base64.StdEncoding.DecodeString(string(privBytes))
		if err != nil {
			return nil, nil, err
		}
		if len(priv) != ed25519.PrivateKeySize {
			return nil, nil, fmt.Errorf("invalid private key length: %d", len(priv))
		}

		privKey := ed25519.PrivateKey(priv)
		return privKey, privKey.Public().(ed25519.PublicKey), nil
	}

	// create new
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}

	if err := os.WriteFile(privPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)), 0644); err != nil {
		return nil, nil, err
	}

	return priv, pub, nil
}

func (keyPair *EdgeKeyPair) OnConnectPublish(ctx context.Context) (*messaging.ConnectMessage, error) {

	spki, err := x509.MarshalPKIXPublicKey(keyPair.PublicKey)
	if err != nil {
		panic(err)
	}

	return &messaging.ConnectMessage{

		Topic:  "identity",
		Qos:    messaging.AtLeastOnce,
		Retain: true,
		Payload: &EdgePublicKeyMessage{
			EdgeID:    keyPair.EdgeID,
			PublicKey: base64.StdEncoding.EncodeToString(spki),
			Algorithm: "ed25519",
			Ts:        time.Now().Unix(),
		},
	}, nil
}
