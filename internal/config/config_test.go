package config

import (
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SiteName == "" {
		t.Fatal("site_name not loaded")
	}
	if _, ok := cfg.Branches["25.12"]; !ok {
		t.Fatalf("quoted branch key 25.12 missing: %+v", cfg.Branches)
	}
	if b := cfg.Branches["SNAPSHOT"]; !b.Snapshot || b.Path != "snapshots" {
		t.Fatalf("unexpected snapshot branch: %+v", b)
	}
	if cfg.Branches["21.02"].Enabled {
		t.Fatal("21.02 should be disabled in the example config")
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("OPENFORGE_LISTEN", "127.0.0.1:9090")
	t.Setenv("OPENFORGE_BACKEND_URL", "http://localhost:8000")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9090" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if cfg.BackendURL != "http://localhost:8000" {
		t.Fatalf("backend = %q", cfg.BackendURL)
	}
}
