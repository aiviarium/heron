//go:build linux

package extension

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// The sandbox runs an extension in new user and mount namespaces, and a new
// network namespace when it declared no hosts. heron re-executes itself
// with sandboxEnv set; the helper makes every mount read-only except the
// files the extension declared it writes, then executes the extension.
const sandboxEnv = "HERON_EXTENSION_SANDBOX"

type sandboxSpec struct {
	Writable []string `json:"writable"`
	Command  []string `json:"command"`
}

func init() {
	if s := os.Getenv(sandboxEnv); s != "" {
		os.Exit(sandboxMain(s))
	}
}

// sandboxed wraps a command so it runs inside the namespaces. It returns
// false where the sandbox can't be used, so the caller warns.
func sandboxed(cmd *exec.Cmd, writable []string, network bool) (bool, error) {
	self, err := os.Executable()
	if err != nil {
		return false, err
	}
	spec, _ := json.Marshal(sandboxSpec{Writable: writable, Command: cmd.Args})
	path, err := exec.LookPath(cmd.Path)
	if err == nil {
		spec, _ = json.Marshal(sandboxSpec{Writable: writable, Command: append([]string{path}, cmd.Args[1:]...)})
	}
	cmd.Path = self
	cmd.Args = []string{self}
	cmd.Env = append(append([]string(nil), cmd.Env...), sandboxEnv+"="+string(spec))
	flags := uintptr(syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS)
	if !network {
		flags |= syscall.CLONE_NEWNET
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 flags,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
		Pdeathsig:                  syscall.SIGKILL,
	}
	return true, nil
}

// sandboxUnavailable reports whether a start failed because namespaces
// aren't allowed here.
func sandboxUnavailable(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "operation not permitted") ||
		strings.Contains(err.Error(), "invalid argument") || strings.Contains(err.Error(), "function not implemented"))
}

var mountFlags = map[string]uintptr{
	"nosuid": syscall.MS_NOSUID, "nodev": syscall.MS_NODEV, "noexec": syscall.MS_NOEXEC,
	"noatime": syscall.MS_NOATIME, "nodiratime": syscall.MS_NODIRATIME, "relatime": syscall.MS_RELATIME,
}

func sandboxMain(raw string) int {
	var spec sandboxSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil || len(spec.Command) == 0 {
		fmt.Fprintln(os.Stderr, "heron sandbox: bad spec")
		return 126
	}
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		fmt.Fprintln(os.Stderr, "heron sandbox: making mounts private:", err)
		return 126
	}
	writable := map[string]bool{}
	for _, w := range spec.Writable {
		if _, err := os.Stat(w); err != nil {
			continue
		}
		if err := syscall.Mount(w, w, "", syscall.MS_BIND|syscall.MS_REC, ""); err == nil {
			writable[w] = true
		}
	}
	under := func(p string) bool {
		for w := range writable {
			if p == w || strings.HasPrefix(p, strings.TrimSuffix(w, "/")+"/") {
				return true
			}
		}
		return false
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		fmt.Fprintln(os.Stderr, "heron sandbox:", err)
		return 126
	}
	type mnt struct {
		path  string
		flags uintptr
	}
	var mounts []mnt
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 {
			continue
		}
		m := mnt{path: unescapeMount(fields[4])}
		for _, o := range strings.Split(fields[5], ",") {
			m.flags |= mountFlags[o]
		}
		mounts = append(mounts, m)
	}
	f.Close()
	for _, m := range mounts {
		if under(m.path) {
			continue
		}
		// Best effort: some mounts, such as those under /proc, can't be
		// remounted, and reading them stays harmless.
		_ = syscall.Mount("", m.path, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|m.flags, "")
	}
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, sandboxEnv+"=") {
			env = append(env, e)
		}
	}
	err = syscall.Exec(spec.Command[0], spec.Command, env)
	fmt.Fprintln(os.Stderr, "heron sandbox: exec:", err)
	return 127
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}
