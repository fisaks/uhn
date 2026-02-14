package blueprint

// BlueprintActivatedMessage is the MQTT payload published to uhn/master/blueprint/activated.
type BlueprintActivatedMessage struct {
	Identifier  string `json:"identifier"`
	Version     int    `json:"version"`
	DownloadURL string `json:"downloadUrl"`
	SHA256      string `json:"sha256"`
	Ts          int64  `json:"ts"`
}

// MasterIdentityMessage is the MQTT payload published to uhn/master/identity.
type MasterIdentityMessage struct {
	PublicKey string `json:"publicKey"`
	Algorithm string `json:"algorithm"`
	Ts        int64  `json:"ts"`
}

// BlueprintVersionFile is persisted locally to track the current blueprint version.
type BlueprintVersionFile struct {
	Identifier string `json:"identifier"`
	Version    int    `json:"version"`
	SHA256     string `json:"sha256"`
}
