package main

import (
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/util"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const (
	cgroupPath         = "/sys/fs/cgroup/uhn-runtime"
	sandboxBaseRootDir = "/tmp/uhn-sandbox"
)

/**
* UHN sandbox guarantees:
* - No host filesystem writes
* - Configurable host network access
* - No privilege escalation
* - Bounded memory and processes
* - Runtime sees only explicitly mounted paths
* - Host paths never visible inside sandbox
 */
func main() {
	if os.Geteuid() != 0 {
		util.Fatal("uhn-sandbox-setup", "must run as root")
	}

	configPath := util.ParseSandboxConfigArgs(os.Args)

	cfg, err := config.LoadSandboxConfig(configPath)
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "Failed to load sandbox config: %v", err)
	}

	setupCgroup(cfg.Limits)

	uid, gid := checkRunAsUser(cfg.RunAsUser)

	pidCh := make(chan int, 1)
	if cfg.Network == config.NetworkDebugAttach {

		hostNS := getHostNetworkNamespace()
		go startDebugForwarderHostNS(hostNS, pidCh, cfg.DebugListen)
	}

	sandboxRoot := createSandboxRootDir()

	initSandboxRoot(sandboxRoot)

	paths := loadHostSandboxPaths()

	mountSandboxFilesystem(sandboxRoot, paths)

	enterSandboxRoot(sandboxRoot)

	hardenSandbox(cfg)

	startSandbox(configPath, paths.Workspace, cfg, pidCh, uid, gid)

}

func startSandbox(
	cfgPath string,
	workspacePath string,
	cfg *config.SandboxConfig,
	pidCh chan<- int,
	uid, gid int,
) {
	execPath := "/usr/lib/uhn/uhn-sandboxd"
	env := util.MergeEnv(config.BaseSandboxEnv(false), cfg.Env)

	if !strings.HasPrefix(cfgPath, workspacePath) {
		util.Fatal("uhn-sandbox-setup",
			"configPath must be inside UHN_WORKSPACE_PATH: %s (workspacePath=%s)",
			cfgPath,
			workspacePath,
		)
	}

	sandboxConfigPath := strings.TrimPrefix(cfgPath, workspacePath)
	if !strings.HasPrefix(sandboxConfigPath, "/") {
		sandboxConfigPath = "/" + sandboxConfigPath
	}

	args := []string{
		"uhn-sandboxd",
		"run",
		"--config", "/uhn-workspace" + sandboxConfigPath,
	}

	if cfg.Network != config.NetworkDebugAttach {
		// we don't need the privileged setup process anymore so drop privileges and
		// swap the setup process with sandboxd binary (same PID, new image).
		dropPrivileges(uid, gid)
		if err := syscall.Exec(
			execPath,
			args,
			env,
		); err != nil {
			util.Fatal("uhn-sandbox-setup", "exec sandbox failed %v", err)
		}
		return
	}
	// start sandboxd as child process under current privileged setup process
	// and keep the setup process alive to forward debug connections from host to sandbox
	cmd := exec.Command(execPath, args[1:]...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
			// Explicitly drop supplementary groups
			Groups: []uint32{},
		},
		Setpgid: true,
	}
	if err := util.AttachAndStartProcessIO(cmd); err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to start sandboxd: %v", err)
	}

	sandboxPid := cmd.Process.Pid

	// start debug forwarder (host netns!)
	select {
	case pidCh <- sandboxPid:
	case <-time.After(2 * time.Second):
		util.Fatal("uhn-sandbox-setup", "debug forwarder did not accept sandbox PID")
	}

	// wait until sandbox exits
	if err := cmd.Wait(); err != nil {
		util.Info("uhn-sandbox-setup", "sandboxd exited: %v", err)
	}
}

func getHostNetworkNamespace() netns.NsHandle {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hostNS, err := netns.Get()
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to get host netns: %v", err)
	}
	return hostNS // we do not close; process lifetime owns it and will be cleaned up on exit by os
}

func startDebugForwarderHostNS(
	hostNS netns.NsHandle,
	pidCh <-chan int,
	debugListen string,
) {

	// Switch to host netns once
	func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := netns.Set(hostNS); err != nil {
			util.Fatal("uhn-sandbox-setup", "failed to switch to host netns: %v", err)
		}
	}()

	listenAddr, _, port, err := util.SplitHostAndPort(debugListen, "0.0.0.0:9250")
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "invalid debug listen address %q: %v", debugListen, err)
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "debug forwarder listen failed: %v", err)
	}
	defer ln.Close()

	sandboxPid := <-pidCh

	for {
		util.Info("uhn-sandbox-setup", "Waiting on debug connection %s", debugListen)
		conn, err := ln.Accept()
		if err != nil {
			util.Error("uhn-sandbox-setup", "Error in debug Accept %v", err)
			return
		}
		util.Info("uhn-sandbox-setup", "Accepted debug connection from %s", conn.RemoteAddr().String())
		go handleDebugConn(conn, hostNS, sandboxPid, port)
	}
}
func handleDebugConn(
	hostConn net.Conn,
	hostNS netns.NsHandle,
	sandboxPid int,
	port int,
) {
	defer hostConn.Close()

	sandboxNS, err := netns.GetFromPid(sandboxPid)
	if err != nil {
		util.Error("uhn-sandbox-setup", "Get sandbox netns failed: %v", err)
		return
	}
	defer sandboxNS.Close()

	sandboxConn, err := func() (net.Conn, error) {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := netns.Set(sandboxNS); err != nil {
			util.Error("uhn-sandbox-setup", "Switch to sandbox netns failed: %v", err)
			return nil, err
		}

		util.Info("uhn-sandbox-setup", "Dialing sandbox debug port %d", port)
		dialer := net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))

		// Always attempt to restore host netns
		if restoreErr := netns.Set(hostNS); restoreErr != nil {
			util.Error("uhn-sandbox-setup", "Failed to restore host netns: %v", restoreErr)
		}
		return conn, err
	}()

	if err != nil {
		util.Error("uhn-sandbox-setup", "Dial sandbox debug port failed: %v", err)
		return
	}
	defer sandboxConn.Close()

	// Normal bidirectional copy (no locked thread) full-duplex streaming
	go func() {
		// Forward from host to sandbox until EOF
		_, _ = io.Copy(sandboxConn, hostConn)
		// if sandboxConn is of type *net.TCPConn we need to close the write side
		// to signal EOF to the sandbox process
		if c, ok := sandboxConn.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
	}()
	// Forward from sandbox to host until EOF
	_, _ = io.Copy(hostConn, sandboxConn)
}

func writeCgroupFile(name, value string) {
	path := filepath.Join(cgroupPath, name)
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to write "+path+" %v", err)
	}
}

func hardenSandbox(cfg *config.SandboxConfig) {
	switch cfg.Network {
	case config.NetworkNone:
		unshareNet()
	case config.NetworkLoopback:
		unshareNet()
		bringUpLoopback()
	case config.NetworkDebugAttach:
		unshareNet()
		bringUpLoopback()
		//setup stays alive to forward debug connections from host port to sandbox
	case config.NetworkFull:
		// do NOT unshare net we can access the whole world
	default:
		util.Fatal("uhn-sandbox-setup", "invalid network mode: %q", cfg.Network)
	}

	// prevent privilege escalation
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		util.Fatal("uhn-sandbox-setup", "prctl no_new_privs failed %v", err)
	}

}

func unshareNet() {
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		util.Fatal("uhn-sandbox-setup", "unshare net namespace failed %v", err)
	}
}

func bringUpLoopback() {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to find loopback interface: %v", err)
	}

	// Idempotent: safe to call even if already up
	if err := netlink.LinkSetUp(lo); err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to bring loopback up: %v", err)
	}
}

func enterSandboxRoot(root string) {
	if err := syscall.Chroot(root); err != nil {
		util.Fatal("uhn-sandbox-setup", "chroot failed %v", err)
	}

	if err := os.Chdir("/"); err != nil {
		util.Fatal("uhn-sandbox-setup", "chdir failed %v", err)
	}
}

func mountSandboxFilesystem(root string, paths SandboxHostPaths) {

	// system mounts (before host-provided, so overlays work)
	bind("/bin", root+"/bin", true)
	bind("/lib", root+"/lib", true)
	bindIfExists("/lib64", root+"/lib64", true)
	bind("/usr/bin", root+"/usr/bin", true)
	bind("/usr/lib", root+"/usr/lib", true)

	// host-provided mounts (overlay on top of system mounts)
	bind(paths.Runtime, root+"/uhn-runtime", true)
	bind(paths.Node, root+"/uhn-node", true)
	bind(paths.Workspace+"/sandbox/current", root+"/uhn-workspace/sandbox/current", true)
	bind(paths.Workspace+"/blueprint/active", root+"/uhn-workspace/blueprint/active", true)
	bind(paths.Sandbox, root+"/usr/lib/uhn", true)

	// tmpfs
	if err := syscall.Mount("tmpfs", root+"/tmp", "tmpfs", 0, "size=128M"); err != nil {
		util.Fatal("uhn-sandbox-setup", "mount tmpfs failed %v", err)
	}

	// proc
	if err := syscall.Mount(
		"proc",
		root+"/proc",
		"proc",
		syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV,
		"",
	); err != nil {
		util.Fatal("uhn-sandbox-setup", "mount proc failed %v", err)
	}

	setupDev(root)
}

type SandboxHostPaths struct {
	Runtime   string // host path mounted as /uhn-runtime
	Workspace string // host path mounted as /uhn-workspace
	Node      string // host node binary / libs
	Sandbox   string // host sandbox root
}

func loadHostSandboxPaths() SandboxHostPaths {
	paths := SandboxHostPaths{
		Runtime:   os.Getenv("UHN_RUNTIME_PATH"),
		Workspace: os.Getenv("UHN_WORKSPACE_PATH"),
		Node:      os.Getenv("UHN_NODE_PATH"),
		Sandbox:   os.Getenv("UHN_SANDBOX_PATH"),
	}

	for k, v := range map[string]string{
		"UHN_RUNTIME_PATH":   paths.Runtime,
		"UHN_WORKSPACE_PATH": paths.Workspace,
		"UHN_NODE_PATH":      paths.Node,
		"UHN_SANDBOX_PATH":   paths.Sandbox,
	} {
		if v == "" {
			util.Fatal("uhn-sandbox-setup", "%s is required", k)
		}
	}

	return paths
}

func checkRunAsUser(runAsUser string) (int, int) {
	u, err := user.Lookup(runAsUser)
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to lookup user %s: %v", runAsUser, err)
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "invalid uid %q for user %s: %v", u.Uid, runAsUser, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		util.Fatal("uhn-sandbox-setup", "invalid gid %q for user %s: %v", u.Gid, runAsUser, err)
	}
	return uid, gid
}

func setupCgroup(limits *config.Limits) {
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to create cgroup directory %v", err)
	}

	if limits != nil && limits.MemoryBytes > 0 {
		writeCgroupFile("memory.max", strconv.FormatInt(limits.MemoryBytes, 10))
	}

	if limits != nil && limits.MaxPids > 0 {
		writeCgroupFile("pids.max", strconv.FormatInt(limits.MaxPids, 10))
	}

	writeCgroupFile("cgroup.procs", strconv.Itoa(os.Getpid()))
}

func dropPrivileges(uid, gid int) {

	if err := syscall.Setgroups([]int{}); err != nil {
		util.Fatal("uhn-sandbox-setup", "setgroups failed %v", err)
	}

	if err := syscall.Setgid(gid); err != nil {
		util.Fatal("uhn-sandbox-setup", "setgid failed %v", err)
	}

	if err := syscall.Setuid(uid); err != nil {
		util.Fatal("uhn-sandbox-setup", "setuid failed %v", err)
	}

}

func bindIfExists(src, dst string, readonly bool) {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return
	}
	bind(src, dst, readonly)
}

func bind(src, dst string, readonly bool) {
	flags := uintptr(syscall.MS_BIND | syscall.MS_REC)
	if err := syscall.Mount(src, dst, "", flags, ""); err != nil {
		util.Fatal("uhn-sandbox-setup", "bind mount %s -> %s failed %v", src, dst, err)
	}

	if readonly {
		if err := syscall.Mount("", dst, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, ""); err != nil {
			util.Fatal("uhn-sandbox-setup", "remount ro %s failed %v", dst, err)
		}
	}
}

func setupDev(root string) {
	if err := syscall.Mount("tmpfs", root+"/dev", "tmpfs",
		syscall.MS_NOSUID|syscall.MS_NOEXEC,
		"mode=755,size=4M"); err != nil {
		util.Fatal("uhn-sandbox-setup", "mount /dev tmpfs failed %v", err)
	}

	nodes := []struct {
		name         string
		major, minor uint32
	}{
		{"null", 1, 3},
		{"zero", 1, 5},
		{"random", 1, 8},
		{"urandom", 1, 9},
	}

	for _, n := range nodes {
		path := filepath.Join(root, "dev", n.name)
		if err := unix.Mknod(
			path,
			unix.S_IFCHR|0666,
			int(unix.Mkdev(n.major, n.minor)),
		); err != nil && err != unix.EEXIST {
			util.Fatal("uhn-sandbox-setup", "mknod %s failed %v", path, err)
		}
	}

	if err := syscall.Mount("", root+"/dev", "",
		syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_NOSUID|syscall.MS_NOEXEC,
		""); err != nil {
		util.Fatal("uhn-sandbox-setup", "remount /dev readonly failed %v", err)
	}

}

func initSandboxRoot(root string) {

	// --- mount namespace ---
	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		util.Fatal("uhn-sandbox-setup", "unshare mount namespace failed %v", err)
	}

	// make mounts private
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to make mounts private %v", err)
	}

	dirs := []string{
		root + "/uhn-runtime",
		root + "/uhn-workspace/sandbox/current",
		root + "/uhn-workspace/blueprint/active",
		root + "/uhn-node",
		root + "/tmp",
		root + "/bin",
		root + "/lib",
		root + "/usr/bin",
		root + "/usr/lib",
		root + "/usr/lib/uhn",
		root + "/proc",
		root + "/dev",
	}

	// /lib64 only exists on glibc-based systems (Debian/Ubuntu), not Alpine
	if _, err := os.Stat("/lib64"); err == nil {
		dirs = append(dirs, root+"/lib64")
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			util.Fatal("uhn-sandbox-setup", "mkdir %s failed %v", d, err)
		}
	}
}

func createSandboxRootDir() string {
	id := strconv.Itoa(os.Getpid())
	root := filepath.Join(sandboxBaseRootDir, id)

	if err := os.MkdirAll(root, 0755); err != nil {
		util.Fatal("uhn-sandbox-setup", "failed to create sandbox root %s: %v", root, err)
	}

	return root
}
