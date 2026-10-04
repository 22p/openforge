package asu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openforge/openforge/internal/config"
)

// Metadata serves OpenWrt release metadata to the firmware selector and to the
// request validator. It mirrors asu/util.py (reload_versions, reload_targets,
// reload_profiles, parse_packages_file ...) with an in-memory, TTL based cache.
type Metadata struct {
	cfg    *config.Config
	http   *http.Client
	logger *slog.Logger

	reloadMu sync.Mutex

	mu          sync.RWMutex
	versions    []string
	latest      []string
	versionsAt  time.Time
	targets     map[string]map[string]string
	targetsAt   map[string]time.Time
	profiles    map[string]map[string]map[string]string
	profilesRaw map[string]map[string][]byte
	profilesAt  map[string]map[string]time.Time
}

// NewMetadata creates a metadata service.
func NewMetadata(cfg *config.Config, logger *slog.Logger) *Metadata {
	return &Metadata{
		cfg:         cfg,
		http:        &http.Client{Timeout: cfg.HTTPTimeout},
		logger:      logger,
		targets:     map[string]map[string]string{},
		targetsAt:   map[string]time.Time{},
		profiles:    map[string]map[string]map[string]string{},
		profilesRaw: map[string]map[string][]byte{},
		profilesAt:  map[string]map[string]time.Time{},
	}
}

func (m *Metadata) upstream() string { return strings.TrimRight(m.cfg.UpstreamURL, "/") }

// BranchName resolves the branch a concrete version belongs to, matching
// asu.util.get_branch.
func (m *Metadata) BranchName(versionOrBranch string) string {
	if _, ok := m.cfg.Branches[versionOrBranch]; ok {
		return versionOrBranch
	}
	if strings.HasSuffix(versionOrBranch, "-SNAPSHOT") {
		idx := strings.LastIndex(versionOrBranch, "-")
		return versionOrBranch[:idx]
	}
	if idx := strings.LastIndex(versionOrBranch, "."); idx >= 0 {
		return versionOrBranch[:idx]
	}
	return versionOrBranch
}

// Branch returns the branch definition for a version.
func (m *Metadata) Branch(versionOrBranch string) (config.Branch, bool) {
	b, ok := m.cfg.Branches[m.BranchName(versionOrBranch)]
	return b, ok
}

// VersionPath expands the upstream path template for a version.
func (m *Metadata) VersionPath(version string) (string, bool) {
	b, ok := m.Branch(version)
	if !ok || b.Path == "" {
		return "", false
	}
	return strings.ReplaceAll(b.Path, "{version}", version), true
}

func (m *Metadata) inSupportedBranch(version string) bool {
	for name, b := range m.cfg.Branches {
		if b.Enabled && strings.HasPrefix(version, name) {
			return true
		}
	}
	return false
}

// Versions returns all versions advertised by this server.
func (m *Metadata) Versions(ctx context.Context) []string {
	if err := m.ensureVersions(ctx); err != nil {
		m.logger.Warn("metadata: versions refresh failed", "err", err)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.versions...)
}

// Latest returns the "latest" version set (stable, oldstable, upcoming).
func (m *Metadata) Latest(ctx context.Context) []string {
	if err := m.ensureVersions(ctx); err != nil {
		m.logger.Warn("metadata: versions refresh failed", "err", err)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.latest...)
}

func (m *Metadata) versionsFresh() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.versions) > 0 && time.Since(m.versionsAt) < m.cfg.MetadataTTL
}

func (m *Metadata) ensureVersions(ctx context.Context) error {
	if m.versionsFresh() {
		return nil
	}
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if m.versionsFresh() {
		return nil
	}

	var payload struct {
		StableVersion    string   `json:"stable_version"`
		OldstableVersion string   `json:"oldstable_version"`
		UpcomingVersion  string   `json:"upcoming_version"`
		VersionsList     []string `json:"versions_list"`
	}
	if err := m.getJSON(ctx, m.upstream()+"/.versions.json", &payload); err != nil {
		return err
	}

	latest := m.addVersions(nil, payload.UpcomingVersion, payload.StableVersion, payload.OldstableVersion)

	versions := m.addVersions(nil, payload.UpcomingVersion)
	versions = m.addVersions(versions, payload.VersionsList...)
	versions = m.addVersions(versions, "SNAPSHOT")
	for name := range m.cfg.Branches {
		if name == "SNAPSHOT" {
			continue
		}
		versions = m.addVersions(versions, name+"-SNAPSHOT")
	}

	sort.SliceStable(versions, func(i, j int) bool {
		return versionKey(versions[i]) > versionKey(versions[j])
	})

	m.mu.Lock()
	m.versions = versions
	m.latest = latest
	m.versionsAt = time.Now()
	m.mu.Unlock()
	return nil
}

func (m *Metadata) addVersions(list []string, versions ...string) []string {
	seen := map[string]bool{}
	for _, v := range list {
		seen[v] = true
	}
	for _, v := range versions {
		if v == "" || seen[v] || !m.inSupportedBranch(v) {
			continue
		}
		seen[v] = true
		list = append(list, v)
	}
	return list
}

// versionKey reproduces the ASU sort key: v.replace(".0-rc", "-rc").
func versionKey(v string) string { return strings.ReplaceAll(v, ".0-rc", "-rc") }

// Targets returns the target -> architecture mapping for a version.
func (m *Metadata) Targets(ctx context.Context, version string) (map[string]string, error) {
	m.mu.RLock()
	cached, at := m.targets[version], m.targetsAt[version]
	m.mu.RUnlock()
	if cached != nil && time.Since(at) < m.cfg.MetadataTTL {
		return cached, nil
	}

	path, ok := m.VersionPath(version)
	if !ok {
		return nil, fmt.Errorf("unsupported version: %s", version)
	}
	out := map[string]string{}
	if err := m.getJSON(ctx, m.upstream()+"/"+path+"/.targets.json", &out); err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.targets[version] = out
	m.targetsAt[version] = time.Now()
	m.mu.Unlock()
	return out, nil
}

// RefreshTargets bypasses the metadata cache for a single version, matching the
// reload-on-miss behaviour of the ASU validator.
func (m *Metadata) RefreshTargets(ctx context.Context, version string) (map[string]string, error) {
	m.mu.Lock()
	delete(m.targets, version)
	delete(m.targetsAt, version)
	m.mu.Unlock()
	return m.Targets(ctx, version)
}

// RefreshProfiles bypasses the metadata cache for a single version/target.
func (m *Metadata) RefreshProfiles(ctx context.Context, version, target string) (map[string]string, error) {
	m.mu.Lock()
	if m.profiles[version] != nil {
		delete(m.profiles[version], target)
	}
	if m.profilesRaw[version] != nil {
		delete(m.profilesRaw[version], target)
	}
	if m.profilesAt[version] != nil {
		delete(m.profilesAt[version], target)
	}
	m.mu.Unlock()
	return m.Profiles(ctx, version, target)
}

// Profiles returns the device-name -> canonical-profile mapping for a version
// and target, matching asu.util.reload_profiles.
func (m *Metadata) Profiles(ctx context.Context, version, target string) (map[string]string, error) {
	raw, err := m.RawProfiles(ctx, version, target)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	if p := m.profiles[version][target]; p != nil {
		m.mu.RUnlock()
		return p, nil
	}
	m.mu.RUnlock()

	built, err := buildProfileMap(raw)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.profiles[version] == nil {
		m.profiles[version] = map[string]map[string]string{}
	}
	m.profiles[version][target] = built
	m.mu.Unlock()
	return built, nil
}

func buildProfileMap(raw []byte) (map[string]string, error) {
	var doc struct {
		Profiles map[string]struct {
			SupportedDevices []string `json:"supported_devices"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for profile, data := range doc.Profiles {
		out[strings.ReplaceAll(profile, ",", "_")] = profile
		for _, name := range data.SupportedDevices {
			out[strings.ReplaceAll(name, ",", "_")] = profile
		}
	}
	return out, nil
}

// RawProfiles returns the raw upstream profiles.json for a version/target.
func (m *Metadata) RawProfiles(ctx context.Context, version, target string) ([]byte, error) {
	m.mu.RLock()
	raw, at := m.profilesRaw[version][target], m.profilesAt[version][target]
	m.mu.RUnlock()
	if raw != nil && time.Since(at) < m.cfg.MetadataTTL {
		return raw, nil
	}

	path, ok := m.VersionPath(version)
	if !ok {
		return nil, fmt.Errorf("unsupported version: %s", version)
	}
	body, err := m.getBytes(ctx, m.upstream()+"/"+path+"/targets/"+target+"/profiles.json")
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.profilesRaw[version] == nil {
		m.profilesRaw[version] = map[string][]byte{}
		m.profilesAt[version] = map[string]time.Time{}
	}
	m.profilesRaw[version][target] = body
	m.profilesAt[version][target] = time.Now()
	m.mu.Unlock()
	return body, nil
}

// ProfileDetail merges release metadata with a single profile entry, matching
// the ASU /json/v1/{path}/targets/{target}/{profile}.json endpoint.
func (m *Metadata) ProfileDetail(ctx context.Context, version, target, profile string) (map[string]any, error) {
	raw, err := m.RawProfiles(ctx, version, target)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	profiles, _ := doc["profiles"].(map[string]any)
	entry, ok := profiles[profile].(map[string]any)
	if !ok {
		return nil, ErrProfileNotFound
	}
	delete(doc, "profiles")

	out := map[string]any{}
	for k, v := range doc {
		out[k] = v
	}
	for k, v := range entry {
		out[k] = v
	}
	out["id"] = profile
	out["build_at"] = formatBuildAt(doc["source_date_epoch"])
	return out, nil
}

// Revision returns the upstream revision for a version/target/subtarget.
func (m *Metadata) Revision(ctx context.Context, version, target, subtarget string) (string, error) {
	path, ok := m.VersionPath(version)
	if !ok {
		return "", ErrUnsupportedVersion
	}
	var doc struct {
		VersionCode string `json:"version_code"`
	}
	url := m.upstream() + "/" + path + "/targets/" + target + "/" + subtarget + "/profiles.json"
	if err := m.getJSON(ctx, url, &doc); err != nil {
		return "", err
	}
	return doc.VersionCode, nil
}

func formatBuildAt(v any) string {
	epoch := int64(0)
	switch n := v.(type) {
	case float64:
		epoch = int64(n)
	case int64:
		epoch = n
	case string:
		if parsed, err := strconv.ParseInt(n, 10, 64); err == nil {
			epoch = parsed
		}
	}
	return time.Unix(epoch, 0).UTC().Format("2006-01-02T15:04:05.000000Z")
}

// PackageIndex serves /json/v1/{path}/index.json, including the post-kmod-split
// handling from asu/util.parse_packages_file. `path` already points at the
// target directory (e.g. releases/25.12.2/targets/x86/64), matching ASU.
func (m *Metadata) PackageIndex(ctx context.Context, path string) (map[string]any, error) {
	basePath := m.upstream() + "/" + path
	out, err := m.parsePackagesFile(ctx, basePath+"/packages")
	if err != nil {
		return nil, err
	}
	if isPostKmodSplit(path) {
		if kernel := m.parseKernelVersion(ctx, basePath+"/profiles.json"); kernel != "" {
			if kmods, err := m.parsePackagesFile(ctx, basePath+"/kmods/"+kernel); err == nil {
				mergePackageMaps(out, kmods)
			}
		}
	}
	return out, nil
}

// ArchIndex serves /json/v1/{path}/{arch}-index.json by merging feed indexes.
func (m *Metadata) ArchIndex(ctx context.Context, path, arch string) (map[string]string, error) {
	feedURL := m.upstream() + "/" + path + "/" + arch
	feeds := m.parseFeedsConf(ctx, feedURL)
	packages := map[string]string{}
	for _, feed := range feeds {
		idx, err := m.parsePackagesFile(ctx, feedURL+"/"+feed)
		if err != nil {
			continue
		}
		for k, v := range toStringMap(idx["packages"]) {
			packages[k] = v
		}
	}
	return packages, nil
}

func (m *Metadata) parsePackagesFile(ctx context.Context, baseURL string) (map[string]any, error) {
	// Prefer the modern v2 index.json.
	if body, err := m.getBytes(ctx, baseURL+"/index.json"); err == nil {
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err == nil {
			version := 1
			if f, ok := doc["version"].(float64); ok {
				version = int(f)
			}
			if version >= 2 {
				delete(doc, "version")
				return doc, nil
			}
			// v1 fallback: try the opkg Packages file first.
			if pkgs, err := m.parseOpkgPackages(ctx, baseURL+"/Packages"); err == nil && pkgs != nil {
				return pkgs, nil
			}
			return doc, nil
		}
	}
	return nil, fmt.Errorf("no package index at %s", baseURL)
}

func (m *Metadata) parseOpkgPackages(ctx context.Context, url string) (map[string]any, error) {
	body, err := m.getBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	packages := map[string]string{}
	architecture := ""
	for _, chunk := range strings.Split(strings.TrimSpace(string(body)), "\n\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(chunk, "\n") {
			if idx := strings.Index(line, ":"); idx > 0 {
				fields[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
			}
		}
		name := fields["Package"]
		if name == "" {
			continue
		}
		if architecture == "" && fields["Architecture"] != "" && fields["Architecture"] != "all" {
			architecture = fields["Architecture"]
		}
		if abi := fields["ABIVersion"]; abi != "" {
			name = strings.TrimSuffix(name, abi)
		}
		packages[name] = fields["Version"]
	}
	return map[string]any{"architecture": architecture, "packages": packages}, nil
}

func (m *Metadata) parseFeedsConf(ctx context.Context, url string) []string {
	body, err := m.getBytes(ctx, url+"/feeds.conf")
	if err != nil {
		return nil
	}
	var feeds []string
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			feeds = append(feeds, parts[1])
		}
	}
	return feeds
}

func (m *Metadata) parseKernelVersion(ctx context.Context, url string) string {
	body, err := m.getBytes(ctx, url)
	if err != nil {
		return ""
	}
	var doc struct {
		LinuxKernel *struct {
			Version  string `json:"version"`
			Release  string `json:"release"`
			Vermagic string `json:"vermagic"`
		} `json:"linux_kernel"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.LinuxKernel == nil {
		return ""
	}
	return doc.LinuxKernel.Version + "-" + doc.LinuxKernel.Release + "-" + doc.LinuxKernel.Vermagic
}

func mergePackageMaps(dst, src map[string]any) {
	dp := toStringMap(dst["packages"])
	for k, v := range toStringMap(src["packages"]) {
		dp[k] = v
	}
	if dp != nil {
		dst["packages"] = dp
	}
}

// toStringMap coerces a decoded JSON object into map[string]string.
func toStringMap(v any) map[string]string {
	switch typed := v.(type) {
	case map[string]string:
		return typed
	case map[string]any:
		out := make(map[string]string, len(typed))
		for k, val := range typed {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	return nil
}

// isPostKmodSplit mirrors asu.util.is_post_kmod_split_build.
func isPostKmodSplit(path string) bool {
	if strings.HasPrefix(path, "snapshots") {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return false
	}
	version := parts[1]
	major := 0
	if idx := strings.Index(version, "."); idx > 0 {
		major, _ = strconv.Atoi(version[:idx])
	}
	if major >= 24 {
		return true
	}
	if major == 23 {
		segments := strings.Split(version, ".")
		minor := segments[len(segments)-1]
		if minor == "05-SNAPSHOT" || minor >= "6" {
			return true
		}
	}
	return false
}

// --- HTTP helpers -----------------------------------------------------------

func (m *Metadata) getJSON(ctx context.Context, url string, out any) error {
	body, err := m.getBytes(ctx, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (m *Metadata) getBytes(ctx context.Context, url string) ([]byte, error) {
	body, status, err := m.fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, status)
	}
	return body, nil
}

func (m *Metadata) fetch(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "OpenForge")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// Errors returned by the metadata service.
var (
	ErrProfileNotFound    = fmt.Errorf("profile not found")
	ErrUnsupportedVersion = fmt.Errorf("unsupported version")
)
