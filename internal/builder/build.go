package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openforge/openforge/internal/asu"
	"github.com/openforge/openforge/internal/config"
)

// PathResolver resolves an upstream download path for a version.
type PathResolver interface {
	VersionPath(version string) (string, bool)
}

// Pipeline runs an OpenWrt ImageBuilder build inside a container. It is the Go
// port of asu/build.py.
type Pipeline struct {
	cfg    *config.Config
	engine Engine
	store  *Store
	paths  PathResolver
	logger *slog.Logger
}

// NewPipeline creates a build pipeline.
func NewPipeline(cfg *config.Config, engine Engine, store *Store, paths PathResolver, logger *slog.Logger) *Pipeline {
	return &Pipeline{cfg: cfg, engine: engine, store: store, paths: paths, logger: logger}
}

// buildFailure carries a user-facing detail and captured stderr.
type buildFailure struct {
	detail string
	stderr string
}

func (e *buildFailure) Error() string { return e.detail }

var (
	reCurrentRevision = regexp.MustCompile(`Current Revision: "(r.+)"`)
	reDefaultPackages = regexp.MustCompile(`Default Packages: (.*)\n`)
)

var signingImageTypes = map[string]bool{
	"sysupgrade": true, "factory": true, "combined": true, "combined-efi": true, "sdcard": true,
}

// Build executes a build job and returns the ASU compatible result document.
func (p *Pipeline) Build(ctx context.Context, job *Job) (map[string]any, error) {
	req := job.Request()
	hash := job.ID
	binDir := p.store.Dir(hash)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}

	timeout := p.cfg.JobTimeoutDuration()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	image := ImageName(p.cfg.BaseContainer, derefStr(req.Target), derefStr(req.Version))

	job.setImagebuilderStatus("container_setup")
	p.logger.Info("pulling imagebuilder", "image", image)
	if err := p.engine.Pull(ctx, image); err != nil {
		if errors.Is(err, ErrImageNotFound) {
			return nil, &buildFailure{detail: fmt.Sprintf(
				"Image not found: %s. If this version was just released, please try again in a few hours as it may take some time to become fully available.",
				image)}
		}
		return nil, err
	}

	env := map[string]string{}
	if IsSnapshotBuild(derefStr(req.Version)) {
		path, _ := p.paths.VersionPath(derefStr(req.Version))
		path = strings.ReplaceAll(path, "{version}", derefStr(req.Version))
		env["TARGET"] = derefStr(req.Target)
		env["VERSION_PATH"] = path
	}

	spec := ContainerSpec{
		Image:           image,
		Name:            "openforge-" + safeName(hash),
		Command:         []string{"sleep", strconv.Itoa(int(timeout.Seconds()))},
		Tmpfs:           []string{"/builder/" + hash},
		Env:             env,
		Network:         p.cfg.ContainerNetwork,
		CapDropAll:      true,
		NoNewPrivileges: true,
	}
	containerID, err := p.engine.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = p.engine.Remove(context.Background(), containerID)
	}()
	if err := p.engine.Start(ctx, containerID); err != nil {
		return nil, err
	}

	exec := func(cmd ...string) ExecResult {
		result, err := p.engine.Exec(ctx, containerID, ExecSpec{Cmd: cmd, User: "buildbot"})
		if err != nil {
			p.logger.Warn("exec failed", "cmd", cmd, "err", err)
		}
		return result
	}

	if IsSnapshotBuild(derefStr(req.Version)) {
		p.logger.Info("running setup.sh")
		result := exec("sh", "setup.sh")
		job.setOutput(result.Stdout, result.Stderr)
		if result.Code != 0 {
			return nil, &buildFailure{detail: fmt.Sprintf("Could not set up ImageBuilder (returncode=%d)", result.Code), stderr: result.Stderr}
		}
	}

	apkMode := false
	if len(req.Repositories) > 0 || p.cfg.CacheURL != "" {
		apkMode = exec("test", "-f", "/builder/repositories").Code == 0
	}
	if err := p.injectFiles(ctx, containerID, req, apkMode); err != nil {
		return nil, err
	}
	if p.cfg.CacheURL != "" {
		repoFile := "repositories.conf"
		if apkMode {
			repoFile = "repositories"
		}
		cacheHost := strings.TrimRight(p.cfg.CacheURL, "/")
		exec("sed", "-i", "s|https://|"+cacheHost+"/|g", repoFile)
	}

	job.setImagebuilderStatus("validate_revision")
	info := exec("make", "info")
	job.setOutput(info.Stdout, info.Stderr)
	if info.Code != 0 {
		return nil, &buildFailure{detail: "Could not run 'make info'", stderr: info.Stderr}
	}

	revision := ""
	if m := reCurrentRevision.FindStringSubmatch(info.Stdout); len(m) > 1 {
		revision = m[1]
	}
	if revision == "" {
		return nil, &buildFailure{detail: "Could not determine ImageBuilder revision", stderr: info.Stdout}
	}
	if req.VersionCode != nil && *req.VersionCode != "" && revision != *req.VersionCode {
		return nil, &buildFailure{detail: fmt.Sprintf("Received incorrect version %s (requested %s)", revision, *req.VersionCode), stderr: info.Stderr}
	}

	defaultPackages := parsePackageLine(reDefaultPackages, info.Stdout)
	profilePackages := parseProfilePackages(info.Stdout, derefStr(req.Profile))
	known := map[string]bool{}
	for _, pkg := range defaultPackages {
		known[pkg] = true
	}
	for _, pkg := range profilePackages {
		known[pkg] = true
	}
	defaultList := make([]string, 0, len(known))
	for pkg := range known {
		defaultList = append(defaultList, pkg)
	}

	ApplyPackageChanges(req)

	buildCmdPackages := req.Packages
	if req.DiffPackages {
		buildCmdPackages = DiffPackages(req.Packages, defaultList)
	}

	job.setImagebuilderStatus("validate_manifest")
	manifestRes := exec("make", "manifest",
		"PROFILE="+derefStr(req.Profile),
		"PACKAGES="+strings.Join(buildCmdPackages, " "),
		"STRIP_ABI=1")
	job.setOutput(manifestRes.Stdout, manifestRes.Stderr)
	if manifestRes.Code != 0 {
		return nil, &buildFailure{detail: CheckPackageErrors(manifestRes.Stderr), stderr: manifestRes.Stderr}
	}
	manifest := ParseManifest(manifestRes.Stdout)
	if errMsg := CheckManifest(manifest, req.PackagesVersions); errMsg != "" {
		return nil, &buildFailure{detail: errMsg, stderr: manifestRes.Stderr}
	}
	packagesHash := asu.GetPackagesHash(mapKeys(manifest))

	buildCmd := []string{
		"make", "image",
		"PROFILE=" + derefStr(req.Profile),
		"PACKAGES=" + strings.Join(buildCmdPackages, " "),
		"EXTRA_IMAGE_NAME=" + packagesHash[:12],
		"BIN_DIR=/builder/" + hash,
	}
	if req.Defaults != nil && *req.Defaults != "" {
		buildCmd = append(buildCmd, "FILES=/builder/asu-files")
	}
	if req.RootfsSizeMB != nil {
		buildCmd = append(buildCmd, fmt.Sprintf("ROOTFS_PARTSIZE=%d", *req.RootfsSizeMB))
	}
	if req.Filesystem != nil && *req.Filesystem != "" {
		buildCmd = append(buildCmd, "ROOTFS_FILESYSTEM="+*req.Filesystem)
	}

	job.setImagebuilderStatus("building_image")
	buildRes := exec(buildCmd...)
	job.setOutput(buildRes.Stdout, buildRes.Stderr)
	if err := p.engine.CopyOut(ctx, containerID, "/builder/"+hash, binDir); err != nil {
		p.logger.Warn("failed to copy artifacts", "hash", hash, "err", err)
	}
	if strings.Contains(buildRes.Stderr, "is too big") || strings.Contains(buildRes.Stderr, "out of space?") {
		return nil, &buildFailure{detail: "Selected packages exceed device storage", stderr: buildRes.Stderr}
	}
	if buildRes.Code != 0 {
		return nil, &buildFailure{detail: "Error while building firmware. See stdout/stderr", stderr: strings.TrimSpace(buildRes.Stderr + "\n" + buildRes.Stdout)}
	}

	profilesPath := filepath.Join(binDir, "profiles.json")
	raw, err := os.ReadFile(profilesPath)
	if err != nil {
		return nil, &buildFailure{detail: "No JSON file found"}
	}
	var jsonContent map[string]any
	if err := json.Unmarshal(raw, &jsonContent); err != nil {
		return nil, &buildFailure{detail: "Invalid JSON file: " + err.Error()}
	}
	profiles, _ := jsonContent["profiles"].(map[string]any)
	profile, ok := profiles[derefStr(req.Profile)].(map[string]any)
	if !ok {
		return nil, &buildFailure{detail: "Profile not found in JSON file"}
	}

	images := collectSigningImages(profile)
	if len(images) > 0 {
		if err := p.signImages(ctx, image, binDir, images); err != nil {
			p.logger.Warn("image signing failed", "err", err)
		}
	}

	delete(jsonContent, "profiles")
	out := map[string]any{}
	for k, v := range jsonContent {
		out[k] = v
	}
	for k, v := range profile {
		out[k] = v
	}
	out["manifest"] = manifest
	out["id"] = derefStr(req.Profile)
	out["bin_dir"] = hash
	out["build_cmd_packages"] = buildCmdPackages
	out["build_at"] = formatBuildAt(jsonContent["source_date_epoch"])
	out["detail"] = "done"
	return out, nil
}

func (p *Pipeline) injectFiles(ctx context.Context, containerID string, req *asu.BuildRequest, apkMode bool) error {
	if len(req.RepositoryKeys) > 0 {
		files := map[string][]byte{}
		for i, key := range req.RepositoryKeys {
			if strings.HasPrefix(strings.TrimSpace(key), "-----BEGIN") {
				files[fmt.Sprintf("keys/custom-%d.pem", i)] = []byte(key)
				continue
			}
			fingerprint, err := FingerprintPubkeyUsign(key)
			if err != nil {
				return fmt.Errorf("invalid signing key: %w", err)
			}
			files["keys/"+fingerprint] = []byte("untrusted comment: " + fingerprint + "\n" + key)
		}
		if len(files) > 0 {
			if err := p.engine.CopyIn(ctx, containerID, "/builder/", files); err != nil {
				return err
			}
		}
	}

	if len(req.Repositories) > 0 {
		repoFile := "repositories.conf"
		if apkMode {
			repoFile = "repositories"
		}
		base := ""
		if req.RepositoriesMode == "append" {
			if result, err := p.engine.Exec(ctx, containerID, ExecSpec{Cmd: []string{"cat", repoFile}, User: "buildbot"}); err == nil {
				base = result.Stdout
			}
		}
		allowed := map[string]string{}
		for name, repoURL := range req.Repositories {
			if asu.IsRepoAllowed(repoURL, p.cfg.RepositoryAllowList) {
				allowed[name] = repoURL
			}
		}
		merged := MergeRepositories(base, allowed, apkMode)
		if err := p.engine.CopyIn(ctx, containerID, "/builder/", map[string][]byte{repoFile: []byte(merged)}); err != nil {
			return err
		}
	}

	if req.Defaults != nil && *req.Defaults != "" {
		if err := p.engine.CopyIn(ctx, containerID, "/builder/", map[string][]byte{
			"asu-files/etc/uci-defaults/99-asu-defaults": []byte(*req.Defaults),
		}); err != nil {
			return err
		}
	}
	return nil
}

// signImages signs the requested images using the configured build key. Signing
// is skipped silently when no key (and ucert) is available.
func (p *Pipeline) signImages(ctx context.Context, image, binDir string, images []string) error {
	key := p.cfg.BuildKeyFile
	if key == "" {
		return nil
	}
	if _, err := os.Stat(key); err != nil {
		return nil
	}
	if _, err := os.Stat(key + ".ucert"); err != nil {
		return nil
	}
	spec := ContainerSpec{
		Image: image,
		Mounts: []Mount{
			{Source: key, Target: "/builder/key-build", ReadOnly: true},
			{Source: key + ".ucert", Target: "/builder/key-build.ucert", ReadOnly: true},
			{Source: binDir, Target: "/work"},
		},
		User:       "root",
		WorkingDir: "/work",
		Env: map[string]string{
			"IMAGES_TO_SIGN": strings.Join(images, " "),
			"PATH":           "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/builder/staging_dir/host/bin",
		},
	}
	containerID, err := p.engine.Create(ctx, spec)
	if err != nil {
		return err
	}
	defer func() { _ = p.engine.Remove(context.Background(), containerID) }()
	if err := p.engine.Start(ctx, containerID); err != nil {
		return err
	}
	script := `for IMAGE in $IMAGES_TO_SIGN; do ` +
		`touch "${IMAGE}.test"; ` +
		`fwtool -t -s /dev/null "$IMAGE"; ` +
		`cp "/builder/key-build.ucert" "$IMAGE.ucert"; ` +
		`usign -S -m "$IMAGE" -s "/builder/key-build" -x "$IMAGE.sig"; ` +
		`ucert -A -c "$IMAGE.ucert" -x "$IMAGE.sig"; ` +
		`fwtool -S "$IMAGE.ucert" "$IMAGE"; ` +
		`done`
	result, err := p.engine.Exec(ctx, containerID, ExecSpec{Cmd: []string{"bash", "-c", script}})
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("signing failed: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

func collectSigningImages(profile map[string]any) []string {
	rawImages, _ := profile["images"].([]any)
	var images []string
	for _, item := range rawImages {
		image, ok := item.(map[string]any)
		if !ok {
			continue
		}
		imageType, _ := image["type"].(string)
		name, _ := image["name"].(string)
		if signingImageTypes[imageType] && name != "" {
			images = append(images, name)
		}
	}
	return images
}

func parsePackageLine(re *regexp.Regexp, output string) []string {
	if m := re.FindStringSubmatch(output); len(m) > 1 {
		return strings.Fields(m[1])
	}
	return nil
}

func parseProfilePackages(output, profile string) []string {
	if profile == "" {
		return nil
	}
	re := regexp.MustCompile(regexp.QuoteMeta(profile) + `:\n    .+\n    Packages: (.*?)\n`)
	if m := re.FindStringSubmatch(output); len(m) > 1 {
		return strings.Fields(m[1])
	}
	return nil
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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

func safeName(hash string) string {
	if len(hash) > 12 {
		hash = hash[:12]
	}
	var b strings.Builder
	for _, c := range hash {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "job"
	}
	return b.String()
}
