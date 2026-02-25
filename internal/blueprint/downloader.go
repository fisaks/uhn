package blueprint

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/encrypt"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

const signedPayload = "GET /api/internal/download/blueprint"

// BlueprintDownloader handles downloading, validating, and storing blueprint zip files.
type BlueprintDownloader struct {
	edgeName              string
	keyPair               *encrypt.EdgeKeyPair
	workspacePath         string
	currentVersion        *BlueprintVersionFile
	masterPubKey          ed25519.PublicKey
	downloading           bool
	pending               *BlueprintActivatedMessage
	httpClient            *http.Client
	broker                messaging.Broker
	mu                    sync.Mutex
	OnBlueprintReady       func() // called after successful download + extract
	OnBlueprintDeactivated func() // called when blueprint is deactivated (null payload)
}

// NewBlueprintDownloader creates a new downloader and loads the current version from disk.
func NewBlueprintDownloader(edgeName string, keyPair *encrypt.EdgeKeyPair, workspacePath string) *BlueprintDownloader {
	d := &BlueprintDownloader{
		edgeName:      edgeName,
		keyPair:       keyPair,
		workspacePath: workspacePath,
		httpClient:    &http.Client{},
	}
	d.currentVersion = d.loadVersionFile()
	if d.currentVersion != nil {
		logging.Info("Loaded blueprint version",
			"identifier", d.currentVersion.Identifier,
			"version", d.currentVersion.Version,
			"sha256", d.currentVersion.SHA256,
		)
	}
	return d
}

// SetBroker sets the MQTT broker used to publish active blueprint state.
func (d *BlueprintDownloader) SetBroker(b messaging.Broker) {
	d.broker = b
}

// publishBlueprint publishes the current blueprint version to blueprint/activated (retained).
// If no blueprint is active, publishes empty bytes to clear the retained message.
func (d *BlueprintDownloader) publishBlueprint() {
	if d.broker == nil {
		return
	}
	ctx := context.Background()
	d.mu.Lock()
	v := d.currentVersion
	d.mu.Unlock()

	if v != nil {
		if err := d.broker.PublishJSON(ctx, "blueprint/activated", messaging.AtLeastOnce, true, v); err != nil {
			logging.Error("Failed to publish active blueprint", "error", err)
		}
	} else {
		if err := d.broker.Publish(ctx, "blueprint/activated", messaging.AtLeastOnce, true, []byte{}); err != nil {
			logging.Error("Failed to clear active blueprint", "error", err)
		}
	}
}

// OnConnectPublish implements messaging.OnConnectPublisher — re-publishes the active blueprint on reconnect.
func (d *BlueprintDownloader) OnConnectPublish(ctx context.Context) (*messaging.ConnectMessage, error) {
	d.mu.Lock()
	v := d.currentVersion
	d.mu.Unlock()

	if v != nil {
		return &messaging.ConnectMessage{
			Topic:   "blueprint/activated",
			Qos:     messaging.AtLeastOnce,
			Retain:  true,
			Payload: v,
		}, nil
	}
	return &messaging.ConnectMessage{
		Topic:        "blueprint/activated",
		Qos:          messaging.AtLeastOnce,
		Retain:       true,
		PayloadBytes: []byte{},
	}, nil
}

// HandleMasterIdentity processes a master identity MQTT message, extracting the SPKI public key.
func (d *BlueprintDownloader) HandleMasterIdentity(payload []byte) {
	var msg MasterIdentityMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("Failed to parse master identity message", "error", err)
		return
	}

	pubKey, err := encrypt.ParseSPKIPublicKey(msg.PublicKey)
	if err != nil {
		logging.Warn("Failed to parse master public key", "error", err)
		return
	}

	d.mu.Lock()
	d.masterPubKey = pubKey
	pending := d.pending
	d.pending = nil
	d.mu.Unlock()

	logging.Info("Master identity received", "algorithm", msg.Algorithm)

	if pending != nil {
		logging.Info("Processing pending blueprint activation")
		d.processActivation(pending)
	}
}

// HandleBlueprintActivated processes a blueprint activated MQTT message.
func (d *BlueprintDownloader) HandleBlueprintActivated(payload []byte) {
	// Null payload means blueprint deactivated
	if len(payload) == 0 || string(payload) == "null" {
		logging.Info("Blueprint deactivated (null payload)")
		d.mu.Lock()
		d.currentVersion = nil
		d.mu.Unlock()
		d.publishBlueprint()
		if d.OnBlueprintDeactivated != nil {
			d.OnBlueprintDeactivated()
		}
		return
	}

	var msg BlueprintActivatedMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("Failed to parse blueprint activated message", "error", err)
		return
	}

	logging.Info("Blueprint activation received",
		"identifier", msg.Identifier,
		"version", msg.Version,
	)

	d.mu.Lock()
	if d.masterPubKey == nil {
		logging.Info("Master key not yet available, storing as pending")
		d.pending = &msg
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	d.processActivation(&msg)
}

func (d *BlueprintDownloader) processActivation(msg *BlueprintActivatedMessage) {
	d.mu.Lock()
	if d.currentVersion != nil &&
		d.currentVersion.SHA256 != "" &&
		d.currentVersion.SHA256 == msg.SHA256 {
		d.mu.Unlock()
		logging.Debug("Blueprint SHA256 already matches, skipping download",
			"sha256", msg.SHA256,
		)
		return
	}

	if d.downloading {
		d.mu.Unlock()
		logging.Debug("Download already in progress, skipping")
		return
	}
	d.downloading = true
	d.mu.Unlock()

	go d.downloadBlueprint(msg)
}

func (d *BlueprintDownloader) downloadBlueprint(msg *BlueprintActivatedMessage) {
	defer func() {
		d.mu.Lock()
		d.downloading = false
		d.mu.Unlock()
	}()

	logging.Info("Downloading blueprint",
		"identifier", msg.Identifier,
		"version", msg.Version,
		"url", msg.DownloadURL,
	)

	// Build signed request
	req, err := http.NewRequest("GET", msg.DownloadURL, nil)
	if err != nil {
		logging.Error("Failed to create download request", "error", err)
		return
	}

	signature := d.keyPair.SignBase64([]byte(signedPayload))
	req.Header.Set("x-uhn-edge-id", d.edgeName)
	req.Header.Set("x-uhn-edge-signature", signature)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		logging.Error("Blueprint download failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logging.Error("Blueprint download returned non-200 status", "status", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logging.Error("Failed to read blueprint response body", "error", err)
		return
	}

	// Validate SHA256 hash
	headerSHA256 := resp.Header.Get("X-UHN-Blueprint-SHA256")
	headerSignature := resp.Header.Get("X-UHN-Blueprint-Signature")

	hash := sha256.Sum256(body)
	computedHex := hex.EncodeToString(hash[:])

	if computedHex != headerSHA256 {
		logging.Error("Blueprint SHA256 mismatch (response header)",
			"expected", headerSHA256,
			"computed", computedHex,
		)
		return
	}
	if computedHex != msg.SHA256 {
		logging.Error("Blueprint SHA256 mismatch (MQTT message)",
			"expected", msg.SHA256,
			"computed", computedHex,
		)
		return
	}

	// Verify master signature over raw hash bytes
	sigBytes, err := base64.StdEncoding.DecodeString(headerSignature)
	if err != nil {
		logging.Error("Failed to decode blueprint signature", "error", err)
		return
	}

	d.mu.Lock()
	masterKey := d.masterPubKey
	d.mu.Unlock()

	if !encrypt.VerifySignature(masterKey, hash[:], sigBytes) {
		logging.Error("Blueprint signature verification failed")
		return
	}

	// Atomic write: temp file + rename
	blueprintDir := filepath.Join(d.workspacePath, "blueprint")
	if err := os.MkdirAll(blueprintDir, 0755); err != nil {
		logging.Error("Failed to create blueprint directory", "error", err)
		return
	}

	tmpFile, err := os.CreateTemp(blueprintDir, "blueprint-*.zip.tmp")
	if err != nil {
		logging.Error("Failed to create temp file", "error", err)
		return
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		logging.Error("Failed to write blueprint temp file", "error", err)
		return
	}
	tmpFile.Close()

	finalPath := filepath.Join(blueprintDir, "blueprint.zip")
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		logging.Error("Failed to rename blueprint file", "error", err)
		return
	}

	// Persist version file
	version := &BlueprintVersionFile{
		Identifier:  msg.Identifier,
		Version:     msg.Version,
		SHA256:      msg.SHA256,
		ActivatedAt: time.Now().Unix(),
	}
	d.saveVersionFile(version)

	d.mu.Lock()
	d.currentVersion = version
	d.mu.Unlock()
	d.publishBlueprint()

	activeDir := filepath.Join(blueprintDir, "active")
	if err := d.extractBlueprint(finalPath, activeDir); err != nil {
		logging.Error("Failed to extract blueprint", "error", err)
		return
	}

	logging.Info("Blueprint downloaded, verified, and extracted",
		"identifier", msg.Identifier,
		"version", msg.Version,
		"path", finalPath,
		"activeDir", activeDir,
	)

	if d.OnBlueprintReady != nil {
		d.OnBlueprintReady()
	}
}

func (d *BlueprintDownloader) extractBlueprint(zipPath, destDir string) error {
	// Remove old contents so active dir always matches the current zip
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("remove old active dir: %w", err)
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create active dir: %w", err)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		// Guard against zip slip
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal path in zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", f.Name, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", f.Name, err)
		}

		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("open %s in zip: %w", f.Name, err)
		}

		dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			src.Close()
			return fmt.Errorf("create %s: %w", f.Name, err)
		}

		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return fmt.Errorf("extract %s: %w", f.Name, err)
		}
		src.Close()
		dst.Close()
	}

	return nil
}

func (d *BlueprintDownloader) loadVersionFile() *BlueprintVersionFile {
	path := filepath.Join(d.workspacePath, "blueprint", "version.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var v BlueprintVersionFile
	if err := json.Unmarshal(data, &v); err != nil {
		logging.Warn("Corrupted version.json, treating as no version", "error", err)
		return nil
	}
	return &v
}

func (d *BlueprintDownloader) saveVersionFile(v *BlueprintVersionFile) {
	path := filepath.Join(d.workspacePath, "blueprint", "version.json")

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		logging.Error("Failed to marshal version file", "error", err)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		logging.Error("Failed to write version file", "error", err)
	}
}
