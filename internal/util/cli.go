package util

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func ParseSandboxConfigArgs(args []string) string {
	if len(args) < 2 || args[1] != "run" {
		Fatal("missing command (expected: %s run)", args[0])
	}
	fs := flag.NewFlagSet("run", flag.ExitOnError)

	configPath := fs.String(
		"config",
		"",
		"Path to sandbox config JSON or '-' for stdin",
	)

	fs.Parse(args[2:])

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "missing required -config flag")
		fs.Usage()
		os.Exit(1)
	}

	return *configPath
}
func Fatal(text string, args ...any) {
	fmt.Fprintf(os.Stderr, text+"\n", args...)
	os.Exit(1)
}

func PrepareSetupSandboxCmd(cmd *exec.Cmd) {
	// stdout / stderr passthrough
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = os.Environ()
}

func MergeEnv(base []string, override []string) []string {
	envMap := make(map[string]string)

	// Base defaults
	for _, e := range base {
		k, v, ok := splitEnv(e)
		if ok {
			envMap[k] = v
		}
	}

	// Overrides
	for _, e := range override {
		k, v, ok := splitEnv(e)
		if ok {
			envMap[k] = v
		}
	}

	// Back to []string
	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}

	return out
}

func splitEnv(e string) (key, value string, ok bool) {
	i := strings.IndexByte(e, '=')
	if i <= 0 {
		return "", "", false
	}
	return e[:i], e[i+1:], true
}
