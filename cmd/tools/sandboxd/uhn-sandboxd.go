package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/util"
	"golang.org/x/sys/unix"
)

func main() {
	configPath := util.ParseSandboxConfigArgs(os.Args)
	cfg, err := config.LoadSandboxConfig(configPath)
	if err != nil {
		util.Fatal("Failed to load sandbox config: %v", err)
	}

	if err := supervise(cfg); err != nil {
		util.Fatal("sandbox failed: %v", err)
	}
}
func supervise(cfg *config.SandboxConfig) error {

	if err := setupSupervisor(); err != nil {
		return err
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)

	// New process group so we can kill the entire tree
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	cmd.Env = util.MergeEnv(config.BaseSandboxEnv(false), cfg.Env)
	cmd.Dir = cfg.Cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return err
	}

	// Signal handling
	sigCh := forwardSignals(pgid)
	defer signal.Stop(sigCh)

	// Wait for child
	err = cmd.Wait()

	// Ensure cleanup
	killProcessGroup(pgid)

	return err
}

func forwardSignals(pgid int) chan os.Signal {

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		for range sigCh {
			killProcessGroup(pgid)
		}
	}()

	return sigCh
}

func setupSupervisor() error {
	// Kill child if sandboxd dies
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0); err != nil {
		return fmt.Errorf("prctl PDEATHSIG failed: %w", err)
	}

	// Become subreaper (collect grandchildren)
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl SUBREAPER failed: %w", err)
	}

	return nil
}

func killProcessGroup(pgid int) {
	// Negative PGID kills the whole group and is safe if already killed
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Give it a moment
	time.Sleep(1 * time.Second)

	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
