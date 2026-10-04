package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ErrImageNotFound is returned by Engine.Pull when the ImageBuilder image does
// not exist upstream.
var ErrImageNotFound = errors.New("image not found")

// Mount is a bind mount for a build container.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// ContainerSpec describes a container to create.
type ContainerSpec struct {
	Image           string
	Name            string
	Command         []string
	Tmpfs           []string
	Mounts          []Mount
	Env             map[string]string
	User            string
	WorkingDir      string
	Network         string
	CapDropAll      bool
	NoNewPrivileges bool
}

// ExecSpec describes a command to run inside a running container.
type ExecSpec struct {
	Cmd  []string
	User string
}

// ExecResult is the captured output of an in-container command.
type ExecResult struct {
	Stdout string
	Stderr string
	Code   int
}

// Engine runs build containers. It is implemented by the Podman/Docker CLI
// engine and can be replaced by a fake in tests.
type Engine interface {
	Name() string
	Available(ctx context.Context) bool
	Pull(ctx context.Context, image string) error
	Create(ctx context.Context, spec ContainerSpec) (string, error)
	Start(ctx context.Context, id string) error
	Exec(ctx context.Context, id string, spec ExecSpec) (ExecResult, error)
	CopyIn(ctx context.Context, id, destDir string, files map[string][]byte) error
	CopyOut(ctx context.Context, id, srcDir, destDir string) error
	Remove(ctx context.Context, id string) error
}

// cliEngine drives podman or docker through their command line interface. Both
// share a compatible subset of create/start/exec/cp/rm semantics. The podman
// and docker commands are used directly; remote operation is selected purely
// through the CONTAINER_HOST / DOCKER_HOST environment variables. podman-remote
// is used only as a fallback when the podman command is not installed.
type cliEngine struct {
	bin    string // resolved executable
	name   string // human readable client name (podman-remote, podman, docker)
	kind   string // "podman" or "docker"
	host   string // service endpoint, may be empty
	envVar string // CONTAINER_HOST or DOCKER_HOST
}

// DetectEngine resolves the container engine described by the configuration.
//
// container_engine is the engine *kind*, not a client binary:
//
//   - "docker"         use the Docker CLI (remote via DOCKER_HOST)
//   - "podman"         use the podman command (remote via CONTAINER_HOST)
//   - "auto" (default) prefers Podman; if container_host points at a Docker
//     socket (or only Docker is installed) it selects Docker
//
// container_host is exported to the chosen client as CONTAINER_HOST (Podman)
// or DOCKER_HOST (Docker); the clients themselves decide local vs. remote from
// it. podman-remote is only used when the podman command is not installed.
func DetectEngine(preference, host string) (Engine, error) {
	kind := engineKind(preference, host)

	switch kind {
	case "docker":
		bin, err := exec.LookPath("docker")
		if err != nil {
			return nil, fmt.Errorf("container_engine=docker but the docker CLI was not found")
		}
		if host == "" {
			host = os.Getenv("DOCKER_HOST")
		}
		return &cliEngine{bin: bin, name: "docker", kind: "docker", host: host, envVar: "DOCKER_HOST"}, nil
	default: // podman
		for _, name := range []string{"podman", "podman-remote"} {
			bin, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			// Only podman-remote requires an endpoint; plain podman runs
			// locally unless the user configured a host.
			if host == "" && name == "podman-remote" {
				host = defaultPodmanHost()
			}
			return &cliEngine{bin: bin, name: name, kind: "podman", host: host, envVar: "CONTAINER_HOST"}, nil
		}
		return nil, fmt.Errorf("container_engine=%s but podman/podman-remote was not found", preference)
	}
}

// engineKind decides between the podman and docker families.
func engineKind(preference, host string) string {
	switch strings.ToLower(strings.TrimSpace(preference)) {
	case "docker":
		return "docker"
	case "podman", "podman-remote", "remote":
		return "podman"
	}
	// auto: a Docker socket implies Docker, otherwise prefer Podman.
	if strings.Contains(strings.ToLower(host), "docker") {
		return "docker"
	}
	if host != "" {
		return "podman"
	}
	if hasBinary("podman-remote") || hasBinary("podman") {
		return "podman"
	}
	if hasBinary("docker") {
		return "docker"
	}
	return "podman"
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// defaultPodmanHost returns a sensible Podman service endpoint when
// podman-remote is used without an explicit host.
func defaultPodmanHost() string {
	if v := os.Getenv("CONTAINER_HOST"); v != "" {
		return v
	}
	if uid := os.Getuid(); uid >= 0 {
		candidate := fmt.Sprintf("unix:///run/user/%d/podman/podman.sock", uid)
		if _, err := os.Stat(strings.TrimPrefix(candidate, "unix://")); err == nil {
			return candidate
		}
	}
	if _, err := os.Stat("/run/podman/podman.sock"); err == nil {
		return "unix:///run/podman/podman.sock"
	}
	return ""
}

// Name implements Engine.
func (e *cliEngine) Name() string { return e.name }

// Host returns the configured service endpoint, if any.
func (e *cliEngine) Host() string { return e.host }

// Available implements Engine.
func (e *cliEngine) Available(ctx context.Context) bool {
	_, _, code, err := e.run(ctx, "info")
	return err == nil && code == 0
}

// Pull implements Engine.
func (e *cliEngine) Pull(ctx context.Context, image string) error {
	_, stderr, code, err := e.run(ctx, "pull", image)
	if err != nil {
		return err
	}
	if code != 0 {
		msg := stderr
		if strings.Contains(msg, "not found") || strings.Contains(msg, "manifest unknown") ||
			strings.Contains(msg, "no such") || strings.Contains(msg, "repository does not exist") {
			return fmt.Errorf("%w: %s", ErrImageNotFound, image)
		}
		return fmt.Errorf("pull %s: %s", image, strings.TrimSpace(msg))
	}
	return nil
}

// Create implements Engine.
func (e *cliEngine) Create(ctx context.Context, spec ContainerSpec) (string, error) {
	args := []string{"create"}
	if spec.Name != "" {
		args = append(args, "--name", spec.Name)
	}
	if spec.CapDropAll {
		args = append(args, "--cap-drop", "all")
	}
	if spec.NoNewPrivileges {
		args = append(args, "--security-opt", "no-new-privileges")
	}
	if spec.Network != "" {
		args = append(args, "--network", spec.Network)
	}
	for _, target := range spec.Tmpfs {
		args = append(args, "--tmpfs", target)
	}
	for _, mount := range spec.Mounts {
		value := fmt.Sprintf("type=bind,source=%s,target=%s", mount.Source, mount.Target)
		if mount.ReadOnly {
			value += ",readonly"
		}
		args = append(args, "--mount", value)
	}
	for _, key := range sortedMapKeys(spec.Env) {
		args = append(args, "-e", key+"="+spec.Env[key])
	}
	if spec.WorkingDir != "" {
		args = append(args, "-w", spec.WorkingDir)
	}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)

	stdout, stderr, code, err := e.run(ctx, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("create container: %s", strings.TrimSpace(stderr))
	}
	id := strings.TrimSpace(stdout)
	if idx := strings.LastIndex(id, "\n"); idx >= 0 {
		id = id[idx+1:]
	}
	return strings.TrimSpace(id), nil
}

// Start implements Engine.
func (e *cliEngine) Start(ctx context.Context, id string) error {
	_, stderr, code, err := e.run(ctx, "start", id)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("start container: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// Exec implements Engine.
func (e *cliEngine) Exec(ctx context.Context, id string, spec ExecSpec) (ExecResult, error) {
	args := []string{"exec"}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	args = append(args, id)
	args = append(args, spec.Cmd...)

	stdout, stderr, code, err := e.run(ctx, args...)
	return ExecResult{Stdout: stdout, Stderr: stderr, Code: code}, err
}

// CopyIn implements Engine by staging files locally and using `cp`.
func (e *cliEngine) CopyIn(ctx context.Context, id, destDir string, files map[string][]byte) error {
	dir, err := os.MkdirTemp("", "openforge-cp-in-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return err
		}
	}
	src := filepath.Clean(dir) + string(os.PathSeparator) + "."
	_, stderr, code, err := e.run(ctx, "cp", src, id+":"+destDir)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("copy into container: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// CopyOut implements Engine by copying a directory out of the container.
func (e *cliEngine) CopyOut(ctx context.Context, id, srcDir, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	src := id + ":" + strings.TrimSuffix(srcDir, "/") + "/."
	_, stderr, code, err := e.run(ctx, "cp", src, destDir)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("copy out of container: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// Remove implements Engine.
func (e *cliEngine) Remove(ctx context.Context, id string) error {
	_, _, _, err := e.run(ctx, "rm", "-f", id)
	return err
}

// run executes the engine binary with the configured service endpoint exported
// through the engine's environment variable (CONTAINER_HOST or DOCKER_HOST).
// A non-zero exit code is reported through the returned code rather than the
// error, which is reserved for failures to start the process at all.
func (e *cliEngine) run(ctx context.Context, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, e.bin, args...)
	if e.host != "" && e.envVar != "" {
		cmd.Env = append(os.Environ(), e.envVar+"="+e.host)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
		}
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
