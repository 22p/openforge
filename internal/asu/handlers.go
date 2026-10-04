package asu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/openforge/openforge/internal/config"
)

// Handlers exposes the ASU compatible HTTP API.
type Handlers struct {
	Cfg     *config.Config
	Meta    *Metadata
	Backend Backend
	Version string
	Logger  *slog.Logger
}

// NewHandlers creates an API handler set.
func NewHandlers(cfg *config.Config, meta *Metadata, backend Backend, version string, logger *slog.Logger) *Handlers {
	return &Handlers{Cfg: cfg, Meta: meta, Backend: backend, Version: version, Logger: logger}
}

// --- build endpoints --------------------------------------------------------

// maxBodyBytes bounds build request bodies (custom defaults can be sizeable).
const maxBodyBytes = 4 << 20

// BuildPost implements POST /api/v1/build.
func (h *Handlers) BuildPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req BuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid request body: "+err.Error())
		return
	}
	req.Normalize()

	if missing := requiredFields(&req); missing != "" {
		writeError(w, http.StatusUnprocessableEntity, "Field required: "+missing)
		return
	}

	if ve := h.Meta.Validate(r.Context(), &req); ve != nil {
		writeError(w, ve.Status, ve.Detail)
		return
	}

	resp, err := h.Backend.Build(r.Context(), &req)
	if err != nil {
		h.Logger.Error("build backend error", "err", err)
		writeError(w, http.StatusBadGateway, "Build backend unavailable: "+err.Error())
		return
	}
	h.relayBuild(w, resp, &req)
}

func requiredFields(req *BuildRequest) string {
	switch {
	case req.Version == nil:
		return "version"
	case req.Target == nil:
		return "target"
	case req.Profile == nil:
		return "profile"
	}
	return ""
}

func (h *Handlers) relayBuild(w http.ResponseWriter, resp *BackendResponse, req *BuildRequest) {
	for _, key := range []string{"X-Imagebuilder-Status", "X-Queue-Position"} {
		if v := resp.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")

	var doc map[string]any
	if err := json.Unmarshal(resp.Body, &doc); err == nil {
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
			if v, ok := doc["request_hash"]; !ok || v == nil || v == "" {
				doc["request_hash"] = req.RequestHash()
			}
			if _, ok := doc["status"]; !ok {
				doc["status"] = resp.StatusCode
			}
		}
		patched, err := json.Marshal(doc)
		if err == nil {
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(patched)
			return
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
}

// BuildGet implements GET/HEAD /api/v1/build/{hash}.
func (h *Handlers) BuildGet(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	resp, err := h.Backend.BuildStatus(r.Context(), hash)
	if err != nil {
		h.Logger.Error("build status backend error", "err", err, "hash", hash)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"status": http.StatusBadGateway,
			"title":  "Bad Gateway",
			"detail": "Build backend unavailable: " + err.Error(),
		})
		return
	}
	relayRaw(w, resp)
}

// Revision implements GET /api/v1/revision/{version}/{target}/{subtarget}.
func (h *Handlers) Revision(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("version")
	target := r.PathValue("target")
	subtarget := r.PathValue("subtarget")

	revision, err := h.Meta.Revision(r.Context(), version, target, subtarget)
	switch {
	case errors.Is(err, ErrUnsupportedVersion):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"detail": "Unsupported version: " + version, "status": 400,
		})
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"detail": "Failed to fetch revision for " + version + "/" + target + "/" + subtarget,
			"status": http.StatusBadGateway,
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"revision": revision})
	}
}

// LatestRedirect implements GET /api/v1/latest.
func (h *Handlers) LatestRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/json/v1/latest.json", http.StatusMovedPermanently)
}

// OverviewRedirect implements GET /api/v1/overview.
func (h *Handlers) OverviewRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/json/v1/overview.json", http.StatusMovedPermanently)
}

// Stats implements GET /api/v1/stats.
func (h *Handlers) Stats(w http.ResponseWriter, r *http.Request) {
	queueLength := 0
	if resp, err := h.Backend.Stats(r.Context()); err == nil {
		var doc struct {
			QueueLength int `json:"queue_length"`
		}
		if json.Unmarshal(resp.Body, &doc) == nil {
			queueLength = doc.QueueLength
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"queue_length": queueLength})
}

// StatsSummary implements GET /api/v1/stats/summary.
func (h *Handlers) StatsSummary(w http.ResponseWriter, r *http.Request) {
	if provider, ok := h.Backend.(StatsProvider); ok {
		writeJSON(w, http.StatusOK, provider.StatsSummary())
		return
	}
	if remote, ok := h.Backend.(*RemoteBackend); ok {
		if resp, err := remote.ProxyStats(r.Context(), "/stats/summary"); err == nil && resp.StatusCode == http.StatusOK {
			relayRaw(w, resp)
			return
		}
	}
	queueLength := 0
	if resp, err := h.Backend.Stats(r.Context()); err == nil {
		var doc struct {
			QueueLength int `json:"queue_length"`
		}
		if json.Unmarshal(resp.Body, &doc) == nil {
			queueLength = doc.QueueLength
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"queue_length": queueLength, "builds_24h": 0})
}

// ProxyStatsEndpoint serves the richer statistics endpoints. Remote backends
// are proxied; built-in backends answer from their own event log; otherwise an
// empty structure is returned.
func (h *Handlers) ProxyStatsEndpoint(remotePath, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if remote, ok := h.Backend.(*RemoteBackend); ok {
			query := ""
			if r.URL.RawQuery != "" {
				query = "?" + r.URL.RawQuery
			}
			if resp, err := remote.ProxyStats(r.Context(), remotePath+query); err == nil {
				relayRaw(w, resp)
				return
			}
		}
		if provider, ok := h.Backend.(StatsProvider); ok {
			switch kind {
			case "builds-per-day":
				writeJSON(w, http.StatusOK, provider.BuildsPerDay())
			case "builds-by-version":
				writeJSON(w, http.StatusOK, provider.BuildsByVersion())
			case "top-packages":
				writeJSON(w, http.StatusOK, provider.TopPackages())
			case "build-errors":
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = io.WriteString(w, provider.BuildErrors())
			}
			return
		}
		if kind == "build-errors" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "No build errors recorded.\n")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"labels": []string{}, "datasets": []any{}})
	}
}

// --- json/v1 endpoints ------------------------------------------------------

// JSONLatest implements GET /json/v1/latest.json.
func (h *Handlers) JSONLatest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"latest": h.Meta.Latest(r.Context())})
}

// JSONOverview implements GET /json/v1/overview.json.
func (h *Handlers) JSONOverview(w http.ResponseWriter, r *http.Request) {
	branches := h.generateBranches(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"latest":       h.Meta.Latest(r.Context()),
		"branches":     branches,
		"upstream_url": h.Cfg.UpstreamURL,
		"server": map[string]any{
			"version":                   h.Version,
			"contact":                   h.Cfg.Contact,
			"allow_defaults":            h.Cfg.AllowDefaults,
			"repository_allow_list":     h.Cfg.RepositoryAllowList,
			"max_custom_rootfs_size_mb": h.Cfg.MaxCustomRootfsSizeMB,
			"max_defaults_length":       h.Cfg.MaxDefaultsLength,
			"backend_url":               h.Backend.Endpoint(),
		},
	})
}

// JSONBranches implements GET /json/v1/branches.json.
func (h *Handlers) JSONBranches(w http.ResponseWriter, r *http.Request) {
	branches := h.generateBranches(r.Context())
	out := make([]map[string]any, 0, len(branches))
	for _, name := range h.orderedBranchNames() {
		if b, ok := branches[name]; ok {
			out = append(out, b)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) generateBranches(ctx context.Context) map[string]map[string]any {
	branches := map[string]map[string]any{}
	for name, b := range h.Cfg.Branches {
		branches[name] = map[string]any{
			"path":     b.Path,
			"enabled":  b.Enabled,
			"snapshot": b.Snapshot,
			"versions": []string{},
			"name":     name,
		}
	}
	for _, version := range h.Meta.Versions(ctx) {
		name := h.Meta.BranchName(version)
		if branch, ok := branches[name]; ok {
			branch["versions"] = append(branch["versions"].([]string), version)
		}
	}
	for name, branch := range branches {
		versions := branch["versions"].([]string)
		if len(versions) == 0 {
			branch["targets"] = map[string]string{}
			continue
		}
		targets, err := h.Meta.Targets(ctx, versions[0])
		if err != nil {
			h.Logger.Warn("metadata: targets unavailable", "branch", name, "err", err)
			targets = map[string]string{}
		}
		branch["targets"] = targets
	}
	return branches
}

func (h *Handlers) orderedBranchNames() []string {
	ordered := h.Cfg.EnabledBranches()
	seen := map[string]bool{}
	for _, name := range ordered {
		seen[name] = true
	}
	rest := make([]string, 0)
	for name := range h.Cfg.Branches {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

// JSONV1 implements the catch-all metadata endpoints below /json/v1/ that
// mirror asu.main: package indexes, profile lists and profile details.
func (h *Handlers) JSONV1(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/json/v1/")

	// {path}/.targets.json - target/arch map for a specific version. ASU only
	// advertises targets for the newest version of each branch, so the selector
	// needs this to explore older releases.
	if strings.HasSuffix(rest, "/.targets.json") {
		version := versionFromPath(strings.TrimSuffix(rest, "/.targets.json"))
		targets, err := h.Meta.Targets(r.Context(), version)
		if err != nil {
			targets = map[string]string{}
		}
		writeJSON(w, http.StatusOK, targets)
		return
	}

	// {path}/index.json
	if strings.HasSuffix(rest, "/index.json") {
		base := strings.TrimSuffix(rest, "/index.json")
		index, err := h.Meta.PackageIndex(r.Context(), base)
		if err != nil {
			writeError(w, http.StatusBadGateway, "Failed to fetch package index: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, index)
		return
	}

	// {path}/{arch}-index.json
	if strings.HasSuffix(rest, "-index.json") {
		trimmed := strings.TrimSuffix(rest, "-index.json")
		idx := strings.LastIndex(trimmed, "/")
		if idx <= 0 {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		path, arch := trimmed[:idx], trimmed[idx+1:]
		packages, err := h.Meta.ArchIndex(r.Context(), path, arch)
		if err != nil {
			writeError(w, http.StatusBadGateway, "Failed to fetch feed index: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, packages)
		return
	}

	// {path}/targets/{target}/profiles.json (raw passthrough for the selector)
	if strings.Contains(rest, "/targets/") && strings.HasSuffix(rest, "/profiles.json") {
		before, target, ok := splitAtLast(rest, "/targets/")
		if !ok {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		target = strings.TrimSuffix(target, "/profiles.json")
		version := versionFromPath(before)
		raw, err := h.Meta.RawProfiles(r.Context(), version, target)
		if err != nil {
			writeError(w, http.StatusBadGateway, "Failed to fetch profiles: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}

	// {path}/targets/{target}/{profile}.json
	if strings.Contains(rest, "/targets/") && strings.HasSuffix(rest, ".json") {
		before, tail, ok := splitAtLast(rest, "/targets/")
		if !ok {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		lastSlash := strings.LastIndex(tail, "/")
		if lastSlash <= 0 {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		target := tail[:lastSlash]
		profile := strings.TrimSuffix(tail[lastSlash+1:], ".json")
		version := versionFromPath(before)

		detail, err := h.Meta.ProfileDetail(r.Context(), version, target, profile)
		if errors.Is(err, ErrProfileNotFound) {
			writeError(w, http.StatusNotFound, "Profile not found: "+profile)
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "Failed to fetch profile: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}

	writeError(w, http.StatusNotFound, "not found")
}

// Store implements GET/HEAD /store/{path}.
func (h *Handlers) Store(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/store/")
	resp, err := h.Backend.Store(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Artifact backend unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// versionFromPath extracts the concrete version from an upstream path such as
// "releases/25.12.2" or "snapshots".
func versionFromPath(path string) string {
	if strings.HasPrefix(path, "snapshots") {
		return "SNAPSHOT"
	}
	if strings.HasPrefix(path, "releases/") {
		return strings.TrimPrefix(path, "releases/")
	}
	// Fall back to the last path element.
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func splitAtLast(s, sep string) (before, after string, ok bool) {
	idx := strings.LastIndex(s, sep)
	if idx < 0 {
		return "", "", false
	}
	return s[:idx], s[idx+len(sep):], true
}

// --- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail, "status": status})
}

func relayRaw(w http.ResponseWriter, resp *BackendResponse) {
	for key, values := range resp.Header {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
}
