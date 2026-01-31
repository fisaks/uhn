package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/util"
)

func main() {
	configPath := util.ParseSandboxConfigArgs(os.Args)

	cfg, err := config.LoadSandboxConfig(configPath)
	if err != nil {
		util.Fatal("uhn-sandbox-launch", "Failed to load sandbox config: %v", err)
	}

	sandboxPath := os.Getenv("UHN_SANDBOX_PATH")
	if sandboxPath == "" {
		sandboxPath = "/usr/lib/uhn"
	}

	configPath, err = config.PersistSandboxConfig(cfg)
	if err != nil {
		util.Fatal("uhn-sandbox-launch", "Failed to persist sandbox config: %v", err)
	}
	setupPath := filepath.Join(sandboxPath, "uhn-sandbox-setup")

	cmd := exec.Command(setupPath, "run", "--config", configPath)
	util.PrepareSetupSandboxCmd(cmd)

	if err := cmd.Run(); err != nil {
		// Preserve exit code when possible
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		util.Fatal("uhn-sandbox-launch", "Failed to run sandbox setup: %v", err)

	}
}
