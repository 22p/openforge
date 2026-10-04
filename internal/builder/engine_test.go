package builder

import (
	"os"
	"path/filepath"
	"testing"
)

func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDetectEnginePodmanUsesPodmanCommand(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "podman"))
	writeExec(t, filepath.Join(dir, "podman-remote"))
	t.Setenv("PATH", dir)

	engine, err := DetectEngine("podman", "")
	if err != nil {
		t.Fatal(err)
	}
	cli := engine.(*cliEngine)
	if cli.Name() != "podman" || cli.kind != "podman" || cli.envVar != "CONTAINER_HOST" {
		t.Fatalf("unexpected engine: name=%s kind=%s env=%s", cli.Name(), cli.kind, cli.envVar)
	}
	if cli.Host() != "" {
		t.Fatalf("plain podman should not force a host, got %q", cli.Host())
	}
}

func TestDetectEnginePodmanWithHostUsesContainerHost(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "podman"))
	t.Setenv("PATH", dir)

	host := "unix:///run/podman/podman.sock"
	engine, err := DetectEngine("auto", host)
	if err != nil {
		t.Fatal(err)
	}
	cli := engine.(*cliEngine)
	if cli.Name() != "podman" || cli.kind != "podman" || cli.Host() != host || cli.envVar != "CONTAINER_HOST" {
		t.Fatalf("unexpected engine: name=%s kind=%s host=%s env=%s", cli.Name(), cli.kind, cli.Host(), cli.envVar)
	}
}

func TestDetectEnginePodmanRemoteFallback(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "podman-remote"))
	t.Setenv("PATH", dir)
	t.Setenv("CONTAINER_HOST", "unix:///test/podman.sock")

	engine, err := DetectEngine("auto", "")
	if err != nil {
		t.Fatal(err)
	}
	cli := engine.(*cliEngine)
	if cli.Name() != "podman-remote" || cli.kind != "podman" {
		t.Fatalf("unexpected engine: name=%s kind=%s", cli.Name(), cli.kind)
	}
	if cli.Host() != "unix:///test/podman.sock" {
		t.Fatalf("host = %q", cli.Host())
	}
}

func TestDetectEngineAutoPrefersPodman(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "podman"))
	writeExec(t, filepath.Join(dir, "docker"))
	t.Setenv("PATH", dir)

	engine, err := DetectEngine("auto", "")
	if err != nil {
		t.Fatal(err)
	}
	if engine.(*cliEngine).kind != "podman" {
		t.Fatalf("auto should prefer podman, got %s", engine.Name())
	}
}

func TestDetectEngineDockerFromHost(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "podman"))
	writeExec(t, filepath.Join(dir, "docker"))
	t.Setenv("PATH", dir)

	host := "unix:///var/run/docker.sock"
	engine, err := DetectEngine("auto", host)
	if err != nil {
		t.Fatal(err)
	}
	cli := engine.(*cliEngine)
	if cli.kind != "docker" || cli.Name() != "docker" || cli.Host() != host || cli.envVar != "DOCKER_HOST" {
		t.Fatalf("unexpected engine: name=%s kind=%s host=%s env=%s", cli.Name(), cli.kind, cli.Host(), cli.envVar)
	}
}

func TestDetectEngineDockerPreference(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "podman"))
	writeExec(t, filepath.Join(dir, "docker"))
	t.Setenv("PATH", dir)

	engine, err := DetectEngine("docker", "")
	if err != nil {
		t.Fatal(err)
	}
	if engine.(*cliEngine).kind != "docker" {
		t.Fatalf("expected docker, got %s", engine.Name())
	}
}

func TestDetectEngineOnlyDocker(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "docker"))
	t.Setenv("PATH", dir)

	engine, err := DetectEngine("auto", "")
	if err != nil {
		t.Fatal(err)
	}
	if engine.Name() != "docker" {
		t.Fatalf("engine = %q, want docker", engine.Name())
	}
}

func TestDetectEngineNone(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := DetectEngine("auto", ""); err == nil {
		t.Fatal("expected an error when no engine is present")
	}
	if _, err := DetectEngine("docker", ""); err == nil {
		t.Fatal("expected an error when docker is absent")
	}
	if _, err := DetectEngine("podman", ""); err == nil {
		t.Fatal("expected an error when podman is absent")
	}
}
