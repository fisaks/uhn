package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fisaks/uhn/internal/logging"
)

func TestMain(m *testing.M) {
	logging.Init()
	os.Exit(m.Run())
}

func TestParseIHCResourceID_Hex(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"0x9F1F3E", 0x9F1F3E},
		{"0X9F1F3E", 0x9F1F3E},
		{"_0x9F1F3E", 0x9F1F3E},
		{"0x3E8B5D", 0x3E8B5D},
	}
	for _, tt := range tests {
		got, err := parseIHCResourceID(tt.input)
		if err != nil {
			t.Errorf("parseIHCResourceID(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseIHCResourceID(%q) = %d (0x%X), want %d (0x%X)", tt.input, got, got, tt.want, tt.want)
		}
	}
}

func TestParseIHCResourceID_Integer(t *testing.T) {
	got, err := parseIHCResourceID("10422366")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 10422366 {
		t.Errorf("got %d, want 10422366", got)
	}
}

func TestParseIHCResourceID_Invalid(t *testing.T) {
	tests := []string{"", "  ", "abc", "0xGGGG"}
	for _, input := range tests {
		_, err := parseIHCResourceID(input)
		if err == nil {
			t.Errorf("parseIHCResourceID(%q) expected error, got nil", input)
		}
	}
}

func TestFormatHexID(t *testing.T) {
	got := FormatHexID(0x9F1F3E)
	want := "0x9F1F3E"
	if got != want {
		t.Errorf("FormatHexID(0x9F1F3E) = %q, want %q", got, want)
	}
}

func TestValidateIHC_OnlyConfig(t *testing.T) {
	credsFile := writeTempCreds(t, `{"ihc1": {"username": "testuser", "password": "testpass"}}`)

	json := `{
		"edge": {"name": "test-edge"},
		"ihcCredentialsFile": "CREDS_PATH",
		"ihcControllers": [{
			"name": "ihc1",
			"host": "127.0.0.1",
			"port": 80,
			"resources": [
				{"resourceId": "0x9F1F3E", "type": "digitalOutput"},
				{"resourceId": "0x9F103C", "type": "digitalInput"}
			]
		}]
	}`
	json = strings.Replace(json, "CREDS_PATH", credsFile, 1)

	cfg, err := LoadEdgeConfigFromReader(strings.NewReader(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.IHCControllers) != 1 {
		t.Fatalf("expected 1 IHC controller, got %d", len(cfg.IHCControllers))
	}

	ctrl := cfg.IHCControllers[0]
	if ctrl.Name != "ihc1" {
		t.Errorf("name = %q, want %q", ctrl.Name, "ihc1")
	}
	if ctrl.Username != "testuser" {
		t.Errorf("username = %q, want %q", ctrl.Username, "testuser")
	}
	if ctrl.WaitTimeoutSec != 10 {
		t.Errorf("waitTimeoutSec = %d, want 10 (default)", ctrl.WaitTimeoutSec)
	}
	if ctrl.MaxConsecutiveErrors != 4 {
		t.Errorf("maxConsecutiveErrors = %d, want 4 (default)", ctrl.MaxConsecutiveErrors)
	}

	if len(ctrl.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(ctrl.Resources))
	}
	if ctrl.Resources[0].ResourceIntID != 0x9F1F3E {
		t.Errorf("resources[0].ResourceIntID = 0x%X, want 0x9F1F3E", ctrl.Resources[0].ResourceIntID)
	}
}

func TestValidateIHC_MissingCredentials(t *testing.T) {
	json := `{
		"edge": {"name": "test-edge"},
		"ihcControllers": [{
			"name": "ihc1",
			"host": "127.0.0.1",
			"resources": [{"resourceId": "0x1234", "type": "digitalOutput"}]
		}]
	}`

	_, err := LoadEdgeConfigFromReader(strings.NewReader(json))
	if err == nil {
		t.Fatal("expected validation error for missing credentials")
	}
	if !strings.Contains(err.Error(), "ihcCredentialsFile") {
		t.Errorf("error should mention credentials: %v", err)
	}
}

func TestValidateIHC_InvalidResourceType(t *testing.T) {
	credsFile := writeTempCreds(t, `{"ihc1": {"username": "u", "password": "p"}}`)

	json := `{
		"edge": {"name": "test-edge"},
		"ihcCredentialsFile": "CREDS_PATH",
		"ihcControllers": [{
			"name": "ihc1",
			"host": "127.0.0.1",
			"resources": [{"resourceId": "0x1234", "type": "invalidType"}]
		}]
	}`
	json = strings.Replace(json, "CREDS_PATH", credsFile, 1)

	_, err := LoadEdgeConfigFromReader(strings.NewReader(json))
	if err == nil {
		t.Fatal("expected validation error for invalid resource type")
	}
	if !strings.Contains(err.Error(), "digitalOutput|digitalInput|analogOutput|analogInput") {
		t.Errorf("error should list valid types: %v", err)
	}
}

func TestValidateIHC_DuplicateResourceID(t *testing.T) {
	credsFile := writeTempCreds(t, `{"ihc1": {"username": "u", "password": "p"}}`)

	json := `{
		"edge": {"name": "test-edge"},
		"ihcCredentialsFile": "CREDS_PATH",
		"ihcControllers": [{
			"name": "ihc1",
			"host": "127.0.0.1",
			"resources": [
				{"resourceId": "0x1234", "type": "digitalOutput"},
				{"resourceId": "0x1234", "type": "digitalInput"}
			]
		}]
	}`
	json = strings.Replace(json, "CREDS_PATH", credsFile, 1)

	_, err := LoadEdgeConfigFromReader(strings.NewReader(json))
	if err == nil {
		t.Fatal("expected validation error for duplicate resource ID")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

func TestValidateIHC_HealthCheck(t *testing.T) {
	credsFile := writeTempCreds(t, `{"ihc1": {"username": "u", "password": "p"}}`)

	json := `{
		"edge": {"name": "test-edge"},
		"ihcCredentialsFile": "CREDS_PATH",
		"ihcControllers": [{
			"name": "ihc1",
			"host": "127.0.0.1",
			"healthCheck": {
				"resources": ["0xABCD", "0xEF01"],
				"intervalSec": 30
			},
			"resources": [{"resourceId": "0x1234", "type": "digitalOutput"}]
		}]
	}`
	json = strings.Replace(json, "CREDS_PATH", credsFile, 1)

	cfg, err := LoadEdgeConfigFromReader(strings.NewReader(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctrl := cfg.IHCControllers[0]
	if ctrl.HealthCheck == nil {
		t.Fatal("healthCheck should not be nil")
	}
	if len(ctrl.HealthCheckResourceIDs) != 2 {
		t.Fatalf("expected 2 health check IDs, got %d", len(ctrl.HealthCheckResourceIDs))
	}
	if ctrl.HealthCheckResourceIDs[0] != 0xABCD {
		t.Errorf("healthCheck[0] = 0x%X, want 0xABCD", ctrl.HealthCheckResourceIDs[0])
	}
	if ctrl.HealthCheck.IntervalSec != 30 {
		t.Errorf("healthCheck.intervalSec = %d, want 30", ctrl.HealthCheck.IntervalSec)
	}
}

func TestValidateNoDeviceSources(t *testing.T) {
	json := `{"edge": {"name": "test-edge"}}`

	_, err := LoadEdgeConfigFromReader(strings.NewReader(json))
	if err == nil {
		t.Fatal("expected validation error for no device sources")
	}
	if !strings.Contains(err.Error(), "at least one device source") {
		t.Errorf("error should mention device sources: %v", err)
	}
}

// writeTempCreds creates a temp credentials JSON file and returns its path.
func writeTempCreds(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp credentials file: %v", err)
	}
	return path
}
