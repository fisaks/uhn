package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/util"
	"golang.org/x/sys/unix"
)

const (
	cgroupPath         = "/sys/fs/cgroup/uhn-runtime"
	sandboxBaseRootDir = "/tmp/uhn-sandbox"
)

func main() {
	if os.Geteuid() != 0 {
		util.Fatal("uhn-sandbox-setup must run as root")
	}
	configPath := util.ParseSandboxConfigArgs(os.Args)

	cfg, err := config.LoadSandboxConfig(configPath)
	if err != nil {
		util.Fatal("Failed to load sandbox config: %v", err)
	}

	setupCgroup(cfg.Limits)

	uid, gid := checkRunAsUser(cfg.RunAsUser)

	sandboxRoot := createSandboxRootDir()

	initSandboxRoot(sandboxRoot)

	paths := loadHostSandboxPaths()

	mountSandboxFilesystem(sandboxRoot, paths)

	enterSandboxRoot(sandboxRoot)

	hardenSandbox()

	dropPrivileges(uid, gid)

	startSandbox(configPath, paths.Workspace, cfg)
}

func startSandbox(
	cfgPath string,
	workspace string,
	cfg *config.SandboxConfig,
) {
	execPath := "/usr/lib/uhn/uhn-sandboxd"
	env := util.MergeEnv(config.BaseSandboxEnv(false), cfg.Env)

	if !strings.HasPrefix(cfgPath, workspace) {
		util.Fatal(
			"configPath must be inside UHN_WORKSPACE_PATH: %s (workspace=%s)",
			cfgPath,
			workspace,
		)
	}

	sandboxConfigPath := strings.TrimPrefix(cfgPath, workspace)
	if !strings.HasPrefix(sandboxConfigPath, "/") {
		sandboxConfigPath = "/" + sandboxConfigPath
	}

	if err := syscall.Exec(
		execPath,
		[]string{
			"uhn-sandboxd",
			"run",
			"--config", "/uhn-workspace" + sandboxConfigPath,
		},
		env,
	); err != nil {
		util.Fatal("exec sandbox failed %v", err)
	}
}

func writeCgroupFile(name, value string) {
	path := filepath.Join(cgroupPath, name)
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		util.Fatal("failed to write "+path+" %v", err)
	}
}

func hardenSandbox() {
	// no network
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		util.Fatal("unshare net namespace failed %v", err)
	}

	// prevent privilege escalation
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		util.Fatal("prctl no_new_privs failed %v", err)
	}
	//unix.Unshare(unix.CLONE_NEWPID)
}

func enterSandboxRoot(root string) {
	if err := syscall.Chroot(root); err != nil {
		util.Fatal("chroot failed %v", err)
	}

	if err := os.Chdir("/"); err != nil {
		util.Fatal("chdir failed %v", err)
	}
}

func mountSandboxFilesystem(root string, paths SandboxHostPaths) {

	// host-provided mounts
	bind(paths.Runtime, root+"/uhn-runtime", true)
	bind(paths.Node, root+"/uhn-node", true)
	bind(paths.Workspace, root+"/uhn-workspace", true)
	bind(paths.Sandbox, root+"/usr/lib/uhn", true)

	// system mounts
	bind("/bin", root+"/bin", true)
	bind("/lib", root+"/lib", true)
	bind("/lib64", root+"/lib64", true)
	bind("/usr/bin", root+"/usr/bin", true)

	// tmpfs
	if err := syscall.Mount("tmpfs", root+"/tmp", "tmpfs", 0, "size=128M"); err != nil {
		util.Fatal("mount tmpfs failed %v", err)
	}

	// proc
	if err := syscall.Mount(
		"proc",
		root+"/proc",
		"proc",
		syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV,
		"",
	); err != nil {
		util.Fatal("mount proc failed %v", err)
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
			util.Fatal("%s is required", k)
		}
	}

	return paths
}

func checkRunAsUser(runAsUser string) (int, int) {
	u, err := user.Lookup(runAsUser)
	if err != nil {
		util.Fatal("failed to lookup user %s: %v", runAsUser, err)
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		util.Fatal("invalid uid %q for user %s: %v", u.Uid, runAsUser, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		util.Fatal("invalid gid %q for user %s: %v", u.Gid, runAsUser, err)
	}
	return uid, gid
}

func setupCgroup(limits *config.Limits) {
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		util.Fatal("failed to create cgroup directory %v", err)
	}

	if limits.MemoryBytes > 0 {
		writeCgroupFile("memory.max", strconv.FormatInt(limits.MemoryBytes, 10))
	}

	if limits.MaxPids > 0 {
		writeCgroupFile("pids.max", strconv.FormatInt(limits.MaxPids, 10))
	}

	writeCgroupFile("cgroup.procs", strconv.Itoa(os.Getpid()))
}

func dropPrivileges(uid, gid int) {

	if err := syscall.Setgroups([]int{}); err != nil {
		util.Fatal("setgroups failed %v", err)
	}

	if err := syscall.Setgid(gid); err != nil {
		util.Fatal("setgid failed %v", err)
	}

	if err := syscall.Setuid(uid); err != nil {
		util.Fatal("setuid failed %v", err)
	}
}

func bind(src, dst string, readonly bool) {
	flags := uintptr(syscall.MS_BIND | syscall.MS_REC)
	if err := syscall.Mount(src, dst, "", flags, ""); err != nil {
		util.Fatal("bind mount %s -> %s failed %v", src, dst, err)
	}

	if readonly {
		if err := syscall.Mount("", dst, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, ""); err != nil {
			util.Fatal("remount ro %s failed %v", dst, err)
		}
	}
}

func setupDev(root string) {
	if err := syscall.Mount("tmpfs", root+"/dev", "tmpfs",
		syscall.MS_NOSUID|syscall.MS_NOEXEC,
		"mode=755,size=4M"); err != nil {
		util.Fatal("mount /dev tmpfs failed %v", err)
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
			util.Fatal("mknod %s failed %v", path, err)
		}
	}
}

func initSandboxRoot(root string) {

	// --- mount namespace ---
	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		util.Fatal("unshare mount namespace failed %v", err)
	}

	// make mounts private
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		util.Fatal("failed to make mounts private %v", err)
	}

	dirs := []string{
		root + "/uhn-runtime",
		root + "/uhn-workspace",
		root + "/uhn-node",
		root + "/tmp",
		root + "/bin",
		root + "/lib",
		root + "/lib64",
		root + "/usr/bin",
		root + "/usr/lib/uhn",
		root + "/proc",
		root + "/dev",
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			util.Fatal("mkdir %s failed %v", d, err)
		}
	}
}

func createSandboxRootDir() string {
	id := strconv.Itoa(os.Getpid())
	root := filepath.Join(sandboxBaseRootDir, id)

	if err := os.MkdirAll(root, 0755); err != nil {
		util.Fatal("failed to create sandbox root %s: %v", root, err)
	}

	return root
}
