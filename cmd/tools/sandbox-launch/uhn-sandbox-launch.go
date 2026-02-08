package main

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

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

	if err := cmd.Start(); err != nil {
		util.Fatal("uhn-sandbox-launch", "Failed to start sandbox setup: %v", err)
	}

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	err = cmd.Wait()
	signal.Stop(sigCh)

	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		util.Fatal("uhn-sandbox-launch", "Failed to run sandbox setup: %v", err)
	}
}
