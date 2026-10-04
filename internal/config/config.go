// Package config loads OpenForge configuration from a TOML file and
// environment variables. The layout intentionally mirrors the configuration
// surface of OpenWrt's Attended Sysupgrade server (ASU) so that an existing
// asu.toml can be reused with minimal changes.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Branch describes a supported OpenWrt release branch.
type Branch struct {
	// Path is the upstream download path template, e.g. "releases/{version}".
	Path string `toml:"path" json:"path"`
	// Enabled controls whether the branch is advertised to clients.
	Enabled bool `toml:"enabled" json:"enabled"`
	// Snapshot marks rolling snapshot branches (setup.sh based ImageBuilders).
	Snapshot bool `toml:"snapshot" json:"snapshot"`
}

// Config is the top level OpenForge configuration.
type Config struct {
	// SiteName is shown in the UI header and documents.
	SiteName string `toml:"site_name" json:"site_name"`
	// Listen is the TCP address the HTTP server binds to.
	Listen string `toml:"listen" json:"listen"`
	// Contact is an administrative contact address shown on the API overview.
	Contact string `toml:"contact" json:"contact"`

	// UpstreamURL points at the OpenWrt download mirror used for metadata.
	UpstreamURL string `toml:"upstream_url" json:"upstream_url"`
	// BackendURL optionally delegates builds to an external ASU server. When
	// empty, OpenForge runs its own built-in ImageBuilder build queue.
	BackendURL string `toml:"backend_url" json:"backend_url"`

	// PublicPath is the directory that holds generated images and job records.
	PublicPath string `toml:"public_path" json:"public_path"`
	// BaseContainer is the ImageBuilder image repository.
	BaseContainer string `toml:"base_container" json:"base_container"`
	// ContainerEngine is "auto", "podman", "podman-remote" or "docker".
	ContainerEngine string `toml:"container_engine" json:"container_engine"`
	// ContainerHost is the Podman service endpoint used by podman-remote,
	// e.g. "unix:///run/podman/podman.sock" or "ssh://user@host". It is passed
	// to the engine as CONTAINER_HOST.
	ContainerHost string `toml:"container_host" json:"container_host"`
	// ContainerNetwork is the network build containers join (optional).
	ContainerNetwork string `toml:"container_network" json:"container_network"`
	// CacheURL optionally rewrites package downloads to a caching proxy.
	CacheURL string `toml:"cache_url" json:"cache_url"`
	// BuildKeyFile is an optional usign/ucert key used to sign images.
	BuildKeyFile string `toml:"build_key_file" json:"build_key_file"`
	// Workers is the number of parallel build slots.
	Workers int `toml:"workers" json:"workers"`
	// MaxPendingJobs rejects new requests when the queue is too long.
	MaxPendingJobs int `toml:"max_pending_jobs" json:"max_pending_jobs"`

	// JobTimeout bounds a single build container's lifetime.
	JobTimeout string `toml:"job_timeout" json:"job_timeout"`
	// BuildTTL caches versioned build results.
	BuildTTL string `toml:"build_ttl" json:"build_ttl"`
	// BuildTTLUnversioned caches results without package versions.
	BuildTTLUnversioned string `toml:"build_ttl_unversioned" json:"build_ttl_unversioned"`
	// BuildDefaultsTTL caches results that embed a defaults script.
	BuildDefaultsTTL string `toml:"build_defaults_ttl" json:"build_defaults_ttl"`
	// BuildFailureTTL caches failed builds.
	BuildFailureTTL string `toml:"build_failure_ttl" json:"build_failure_ttl"`

	// AllowDefaults exposes the custom UCI-defaults script feature.
	AllowDefaults bool `toml:"allow_defaults" json:"allow_defaults"`
	// MaxCustomRootfsSizeMB limits CONFIG_TARGET_ROOTFS_PARTSIZE requests.
	MaxCustomRootfsSizeMB int `toml:"max_custom_rootfs_size_mb" json:"max_custom_rootfs_size_mb"`
	// MaxDefaultsLength limits the size of custom defaults scripts (bytes).
	MaxDefaultsLength int `toml:"max_defaults_length" json:"max_defaults_length"`
	// RepositoryAllowList holds URL prefixes for custom apk/opkg repositories.
	RepositoryAllowList []string `toml:"repository_allow_list" json:"repository_allow_list"`

	// Branches maps a branch name (e.g. "24.10") to its definition.
	Branches map[string]Branch `toml:"branches" json:"branches"`

	// MetadataTTL controls how long upstream metadata is cached in memory.
	MetadataTTL time.Duration `toml:"metadata_ttl" json:"metadata_ttl"`
	// HTTPTimeout is the timeout for upstream and backend requests.
	HTTPTimeout time.Duration `toml:"http_timeout" json:"http_timeout"`

	// ServerStats toggles the statistics endpoints and dashboard.
	ServerStats bool `toml:"server_stats" json:"server_stats"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `toml:"log_level" json:"log_level"`
}

// Default returns a configuration with sensible defaults matching ASU.
func Default() *Config {
	return &Config{
		SiteName:              "OpenForge",
		Listen:                ":8080",
		Contact:               "",
		UpstreamURL:           "https://downloads.openwrt.org",
		BackendURL:            "",
		PublicPath:            "public",
		BaseContainer:         "ghcr.io/openwrt/imagebuilder",
		ContainerEngine:       "auto",
		ContainerNetwork:      "",
		CacheURL:              "",
		BuildKeyFile:          "",
		Workers:               1,
		MaxPendingJobs:        200,
		JobTimeout:            "10m",
		BuildTTL:              "7d",
		BuildTTLUnversioned:   "24h",
		BuildDefaultsTTL:      "30m",
		BuildFailureTTL:       "1h",
		AllowDefaults:         false,
		MaxCustomRootfsSizeMB: 1024,
		MaxDefaultsLength:     20480,
		RepositoryAllowList:   []string{},
		Branches: map[string]Branch{
			"SNAPSHOT": {Path: "snapshots", Enabled: true, Snapshot: true},
			"25.12":    {Path: "releases/{version}", Enabled: true},
			"24.10":    {Path: "releases/{version}", Enabled: true},
			"23.05":    {Path: "releases/{version}", Enabled: true},
			"22.03":    {Path: "releases/{version}", Enabled: true},
			"21.02":    {Path: "releases/{version}", Enabled: false},
		},
		MetadataTTL: 10 * time.Minute,
		HTTPTimeout: 30 * time.Second,
		ServerStats: true,
		LogLevel:    "info",
	}
}

// Load reads a TOML configuration file (if it exists) and applies environment
// variable overrides. An empty path, or a path that does not exist, simply
// yields the defaults, which makes `openforge` usable with zero configuration.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, cfg); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	applyEnv(cfg)

	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.MetadataTTL <= 0 {
		cfg.MetadataTTL = 10 * time.Minute
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	if cfg.PublicPath == "" {
		cfg.PublicPath = "public"
	}
	if cfg.BaseContainer == "" {
		cfg.BaseContainer = "ghcr.io/openwrt/imagebuilder"
	}
	if cfg.ContainerEngine == "" {
		cfg.ContainerEngine = "auto"
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.MaxPendingJobs < 0 {
		cfg.MaxPendingJobs = 0
	}
	return cfg, nil
}

// parseDuration extends time.ParseDuration with a day suffix, so values such as
// "7d" (used by ASU) are accepted alongside "24h".
func parseDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(value, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(value)
}

func durationOrDefault(value string, fallback time.Duration) time.Duration {
	if d, err := parseDuration(value); err == nil && d > 0 {
		return d
	}
	return fallback
}

// BuildTTLDuration returns the cache lifetime for versioned build results.
func (c *Config) BuildTTLDuration() time.Duration {
	return durationOrDefault(c.BuildTTL, 7*24*time.Hour)
}

// BuildTTLUnversionedDuration is the lifetime for results without versions.
func (c *Config) BuildTTLUnversionedDuration() time.Duration {
	return durationOrDefault(c.BuildTTLUnversioned, 24*time.Hour)
}

// BuildDefaultsTTLDuration is the lifetime for results with a defaults script.
func (c *Config) BuildDefaultsTTLDuration() time.Duration {
	return durationOrDefault(c.BuildDefaultsTTL, 30*time.Minute)
}

// BuildFailureTTLDuration is the lifetime for failed builds.
func (c *Config) BuildFailureTTLDuration() time.Duration {
	return durationOrDefault(c.BuildFailureTTL, time.Hour)
}

// JobTimeoutDuration bounds a single build container's lifetime.
func (c *Config) JobTimeoutDuration() time.Duration {
	return durationOrDefault(c.JobTimeout, 10*time.Minute)
}

func applyEnv(cfg *Config) {
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := os.LookupEnv(key); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				*dst = b
			}
		}
	}
	integer := func(key string, dst *int) {
		if v, ok := os.LookupEnv(key); ok {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	duration := func(key string, dst *time.Duration) {
		if v, ok := os.LookupEnv(key); ok {
			if d, err := time.ParseDuration(v); err == nil {
				*dst = d
			}
		}
	}

	str("OPENFORGE_SITE_NAME", &cfg.SiteName)
	str("OPENFORGE_LISTEN", &cfg.Listen)
	str("OPENFORGE_CONTACT", &cfg.Contact)
	str("OPENFORGE_UPSTREAM_URL", &cfg.UpstreamURL)
	str("OPENFORGE_BACKEND_URL", &cfg.BackendURL)
	str("OPENFORGE_PUBLIC_PATH", &cfg.PublicPath)
	str("OPENFORGE_BASE_CONTAINER", &cfg.BaseContainer)
	str("OPENFORGE_CONTAINER_ENGINE", &cfg.ContainerEngine)
	str("OPENFORGE_CONTAINER_HOST", &cfg.ContainerHost)
	str("OPENFORGE_CONTAINER_NETWORK", &cfg.ContainerNetwork)
	str("OPENFORGE_CACHE_URL", &cfg.CacheURL)
	str("OPENFORGE_BUILD_KEY_FILE", &cfg.BuildKeyFile)
	str("OPENFORGE_JOB_TIMEOUT", &cfg.JobTimeout)
	str("OPENFORGE_BUILD_TTL", &cfg.BuildTTL)
	str("OPENFORGE_BUILD_TTL_UNVERSIONED", &cfg.BuildTTLUnversioned)
	str("OPENFORGE_BUILD_DEFAULTS_TTL", &cfg.BuildDefaultsTTL)
	str("OPENFORGE_BUILD_FAILURE_TTL", &cfg.BuildFailureTTL)
	integer("OPENFORGE_WORKERS", &cfg.Workers)
	integer("OPENFORGE_MAX_PENDING_JOBS", &cfg.MaxPendingJobs)
	boolean("OPENFORGE_ALLOW_DEFAULTS", &cfg.AllowDefaults)
	boolean("OPENFORGE_SERVER_STATS", &cfg.ServerStats)
	integer("OPENFORGE_MAX_ROOTFS_SIZE_MB", &cfg.MaxCustomRootfsSizeMB)
	integer("OPENFORGE_MAX_DEFAULTS_LENGTH", &cfg.MaxDefaultsLength)
	duration("OPENFORGE_METADATA_TTL", &cfg.MetadataTTL)
	duration("OPENFORGE_HTTP_TIMEOUT", &cfg.HTTPTimeout)
	str("OPENFORGE_LOG_LEVEL", &cfg.LogLevel)

	// ASU compatible environment names, taken from the upstream project.
	str("UPSTREAM_URL", &cfg.UpstreamURL)
	boolean("ALLOW_DEFAULTS", &cfg.AllowDefaults)

	if v, ok := os.LookupEnv("OPENFORGE_REPOSITORY_ALLOW_LIST"); ok && v != "" {
		var list []string
		for _, item := range strings.Split(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				list = append(list, item)
			}
		}
		cfg.RepositoryAllowList = list
	}
	if v, ok := os.LookupEnv("REPOSITORY_ALLOW_LIST"); ok && v != "" {
		var list []string
		for _, item := range strings.Split(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				list = append(list, item)
			}
		}
		cfg.RepositoryAllowList = list
	}
}

// EnabledBranches returns branch names that are enabled, sorted newest first
// according to the order in which they should be presented.
func (c *Config) EnabledBranches() []string {
	order := []string{"SNAPSHOT", "25.12", "24.10", "23.05", "22.03", "21.02", "19.07"}
	seen := map[string]bool{}
	var out []string
	for _, name := range order {
		if b, ok := c.Branches[name]; ok && b.Enabled {
			out = append(out, name)
			seen[name] = true
		}
	}
	// Include any additional custom branches that were configured.
	for name, b := range c.Branches {
		if b.Enabled && !seen[name] {
			out = append(out, name)
		}
	}
	return out
}
