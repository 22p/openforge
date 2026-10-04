package builder

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openforge/openforge/internal/asu"
	"github.com/openforge/openforge/internal/config"
)

func strptr(s string) *string { return &s }

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- pure helpers -----------------------------------------------------------

func TestContainerVersionTag(t *testing.T) {
	cases := map[string]string{
		"1.0.0":          "v1.0.0",
		"SNAPSHOT":       "master",
		"1.0.0-SNAPSHOT": "openwrt-1.0.0",
		"23.05.0-rc3":    "v23.05.0-rc3",
		"SNAPP-SNAPSHOT": "openwrt-SNAPP",
		"24.10.8":        "v24.10.8",
		"24.10-SNAPSHOT": "openwrt-24.10",
	}
	for version, want := range cases {
		if got := ContainerVersionTag(version); got != want {
			t.Errorf("ContainerVersionTag(%q) = %q, want %q", version, got, want)
		}
	}
}

func TestIsSnapshotBuild(t *testing.T) {
	cases := map[string]bool{
		"23.05-SNAPSHOT": true,
		"24.10.0-rc1":    false,
		"24.10.2":        false,
		"24.10-SNAPSHOT": true,
		"SNAPSHOT":       true,
	}
	for version, want := range cases {
		if got := IsSnapshotBuild(version); got != want {
			t.Errorf("IsSnapshotBuild(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestDiffPackages(t *testing.T) {
	cases := []struct {
		requested []string
		defaults  []string
		want      []string
	}{
		{[]string{"test1"}, []string{"test1", "test2"}, []string{"-test2", "test1"}},
		{[]string{"test1"}, []string{"test1"}, []string{"test1"}},
		{[]string{"test1"}, []string{"test2", "test3"}, []string{"-test2", "-test3", "test1"}},
		{[]string{"test1"}, []string{"test2", "-test3"}, []string{"-test2", "-test3", "test1"}},
		{[]string{"z", "x"}, []string{"x", "y", "z"}, []string{"-y", "z", "x"}},
		{[]string{"y", "z"}, []string{"x", "y", "z"}, []string{"-x", "y", "z"}},
	}
	for _, tc := range cases {
		got := DiffPackages(tc.requested, tc.defaults)
		if !equalSlices(got, tc.want) {
			t.Errorf("DiffPackages(%v, %v) = %v, want %v", tc.requested, tc.defaults, got, tc.want)
		}
	}
}

func TestParseManifestOpkgAndApk(t *testing.T) {
	opkg := ParseManifest("test - 1.0\ntest2 - 2.0\n")
	if opkg["test"] != "1.0" || opkg["test2"] != "2.0" {
		t.Fatalf("opkg manifest = %v", opkg)
	}
	apk := ParseManifest("test 1.0\ntest2 2.0\n")
	if apk["test"] != "1.0" || apk["test2"] != "2.0" {
		t.Fatalf("apk manifest = %v", apk)
	}
}

func TestCheckManifest(t *testing.T) {
	if got := CheckManifest(map[string]string{"test": "1.0"}, map[string]string{"test": "1.0"}); got != "" {
		t.Fatalf("valid manifest reported %q", got)
	}
	want := "Impossible package selection: test version not as requested: 2.0 vs. 1.0"
	if got := CheckManifest(map[string]string{"test": "1.0"}, map[string]string{"test": "2.0"}); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	want = "Impossible package selection: test2 not in manifest"
	if got := CheckManifest(map[string]string{"test": "1.0"}, map[string]string{"test2": "1.0"}); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCheckPackageErrors(t *testing.T) {
	if got := CheckPackageErrors("hello world"); got != "Impossible package selection" {
		t.Fatalf("got %q", got)
	}
	if got := CheckPackageErrors(" * opkg_install_cmd: Cannot install package OPKG-MISSING."); got != "Impossible package selection: missing (OPKG-MISSING)" {
		t.Fatalf("got %q", got)
	}
	doc, err := os.ReadFile(filepath.Join("testdata", "package_errors.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "Impossible package selection:" +
		" missing (APK-MISSING, OPKG-MISSING)" +
		" conflicts (APK-CONFLICT-1, APK-CONFLICT-2, APK-CONFLICT-3, APK-CONFLICT-4, OPKG-CONFLICT-1, OPKG-CONFLICT-2, OPKG-CONFLICT-3, OPKG-CONFLICT-4)" +
		" wget-fails (WGET-FAIL-1, WGET-FAIL-2)"
	if got := CheckPackageErrors(string(doc)); got != want {
		t.Fatalf("docstring vector mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestFingerprintPubkeyUsign(t *testing.T) {
	got, err := FingerprintPubkeyUsign("RWSrHfFmlHslUcLbXFIRp+eEikWF9z1N77IJiX5Bt/nJd1a/x+L+SU89")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ab1df166947b2551" {
		t.Fatalf("fingerprint = %q", got)
	}
}

func TestMergeRepositories(t *testing.T) {
	extra := map[string]string{"libremesh": "https://example.org/packages"}
	opkg := MergeRepositories("src/gz base https://x/base\n", extra, false)
	for _, want := range []string{"src/gz base https://x/base", "src/gz libremesh https://example.org/packages", "src imagebuilder file:packages", "option check_signature"} {
		if !containsLine(opkg, want) {
			t.Errorf("opkg merge missing %q in:\n%s", want, opkg)
		}
	}
	apk := MergeRepositories("", extra, true)
	if apk != "https://example.org/packages\n" {
		t.Fatalf("apk merge = %q", apk)
	}
}

func TestApplyPackageChanges(t *testing.T) {
	// 24.10: auc is replaced by owut.
	req := &asu.BuildRequest{
		Version: strptr("24.10.8"), Target: strptr("x86/64"), Profile: strptr("generic"),
		Packages: []string{"auc", "vim"},
	}
	ApplyPackageChanges(req)
	if !equalSlices(req.Packages, []string{"vim", "owut"}) {
		t.Fatalf("24.10 changes = %v", req.Packages)
	}

	// Language pack rename + removal of -en translations.
	req = &asu.BuildRequest{
		Version: strptr("24.10.8"), Target: strptr("x86/64"), Profile: strptr("generic"),
		Packages: []string{"luci-i18n-opkg-base", "luci-i18n-foo-en"},
	}
	ApplyPackageChanges(req)
	if !equalSlices(req.Packages, []string{"luci-i18n-package-manager-base"}) {
		t.Fatalf("language changes = %v", req.Packages)
	}

	// Snapshot: obsolete kmods removed.
	req = &asu.BuildRequest{
		Version: strptr("SNAPSHOT"), Target: strptr("x86/64"), Profile: strptr("generic"),
		Packages: []string{"kmod-nf-conntrack6", "kmod-lib-crc32c", "vim"},
	}
	ApplyPackageChanges(req)
	if !equalSlices(req.Packages, []string{"vim"}) {
		t.Fatalf("snapshot changes = %v", req.Packages)
	}
}

// --- pipeline ---------------------------------------------------------------

func TestEventLogPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.json")

	log := NewEventLog(path)
	log.RecordRequest()
	req := &asu.BuildRequest{
		Version: strptr("24.10.8"), Target: strptr("x86/64"), Profile: strptr("generic"),
		Packages: []string{"vim"},
	}
	req.Normalize()
	log.RecordSuccess(newJob(req.RequestHash(), req))

	log.Close() // must flush synchronously before the process exits

	reopened := NewEventLog(path)
	defer reopened.Close()
	if got := reopened.Successes24h(); got != 1 {
		t.Fatalf("successes after reopen = %d, want 1", got)
	}
	if top := reopened.TopPackages(); len(top["packages"].([]map[string]any)) != 1 {
		t.Fatalf("top packages not persisted: %v", top)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsLine(haystack, needle string) bool {
	sorted := 0
	for _, line := range splitLines(haystack) {
		if line == needle {
			sorted++
		}
	}
	return sorted > 0
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

const fakeProfilesJSON = `{
  "version_code": "r12647-cb44ab4f5d",
  "source_date_epoch": "1612136917",
  "arch_packages": "testarch",
  "profiles": {
    "testprofile": {
      "device_packages": ["kmod-test"],
      "images": [
        {"name": "openwrt-testtarget-squashfs-sysupgrade.bin", "sha256": "abc", "type": "sysupgrade", "filesystem": "squashfs"},
        {"name": "openwrt-testtarget-kernel.bin", "sha256": "def", "type": "kernel"}
      ]
    }
  }
}`

const fakeMakeInfo = `Current Revision: "r12647-cb44ab4f5d"
Default Packages: base-files busybox
testprofile:
    Device: Test
    Packages: kmod-test
`

type fakeEngine struct {
	info     string
	manifest string
	copyOut  map[string]string
	calls    []string
	pulled   bool
	removed  bool
}

func (f *fakeEngine) Name() string                   { return "fake" }
func (f *fakeEngine) Available(context.Context) bool { return true }
func (f *fakeEngine) Pull(context.Context, string) error {
	f.pulled = true
	return nil
}
func (f *fakeEngine) Create(context.Context, ContainerSpec) (string, error) { return "cid", nil }
func (f *fakeEngine) Start(context.Context, string) error                   { return nil }
func (f *fakeEngine) Exec(_ context.Context, _ string, spec ExecSpec) (ExecResult, error) {
	f.calls = append(f.calls, join(spec.Cmd))
	if len(spec.Cmd) >= 2 && spec.Cmd[0] == "make" {
		switch spec.Cmd[1] {
		case "info":
			return ExecResult{Stdout: f.info}, nil
		case "manifest":
			return ExecResult{Stdout: f.manifest}, nil
		case "image":
			return ExecResult{Code: 0}, nil
		}
	}
	return ExecResult{}, nil
}
func (f *fakeEngine) CopyIn(context.Context, string, string, map[string][]byte) error { return nil }
func (f *fakeEngine) CopyOut(_ context.Context, _ string, _ string, dest string) error {
	for name, content := range f.copyOut {
		full := filepath.Join(dest, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeEngine) Remove(context.Context, string) error { f.removed = true; return nil }

func join(cmd []string) string {
	out := ""
	for i, c := range cmd {
		if i > 0 {
			out += " "
		}
		out += c
	}
	return out
}

type staticResolver struct{}

func (staticResolver) VersionPath(version string) (string, bool) {
	if version == "SNAPSHOT" {
		return "snapshots", true
	}
	return "releases/" + version, true
}

func newTestPipeline(t *testing.T, engine Engine) (*Pipeline, *Store, *config.Config) {
	t.Helper()
	cfg := config.Default()
	cfg.PublicPath = t.TempDir()
	cfg.Workers = 1
	store, err := NewStore(cfg.PublicPath)
	if err != nil {
		t.Fatal(err)
	}
	return NewPipeline(cfg, engine, store, staticResolver{}, testLogger()), store, cfg
}

func TestPipelineBuildSuccess(t *testing.T) {
	engine := &fakeEngine{
		info:     fakeMakeInfo,
		manifest: "base-files - 1.0\nbusybox - 1.2\nvim - 1.0\n",
		copyOut:  map[string]string{"profiles.json": fakeProfilesJSON},
	}
	pipeline, _, _ := newTestPipeline(t, engine)

	req := &asu.BuildRequest{
		Version:      strptr("24.10.8"),
		Target:       strptr("testtarget/testsubtarget"),
		Profile:      strptr("testprofile"),
		Packages:     []string{"vim"},
		DiffPackages: true,
	}
	req.Normalize()
	job := newJob(req.RequestHash(), req)
	job.markRunning()

	result, err := pipeline.Build(context.Background(), job)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if !engine.pulled || !engine.removed {
		t.Fatalf("engine lifecycle incomplete: pulled=%v removed=%v", engine.pulled, engine.removed)
	}
	if result["bin_dir"] != job.ID {
		t.Fatalf("bin_dir = %v", result["bin_dir"])
	}
	if result["id"] != "testprofile" {
		t.Fatalf("id = %v", result["id"])
	}
	manifest, _ := result["manifest"].(map[string]string)
	if manifest["vim"] != "1.0" {
		t.Fatalf("manifest = %v", manifest)
	}
	images, _ := result["images"].([]any)
	if len(images) != 2 {
		t.Fatalf("images = %v", images)
	}
	if result["build_at"] != "2021-01-31T23:48:37.000000Z" {
		t.Fatalf("build_at = %v", result["build_at"])
	}
}

func TestPipelineVersionCodeMismatch(t *testing.T) {
	engine := &fakeEngine{info: fakeMakeInfo, manifest: "vim - 1.0\n", copyOut: map[string]string{}}
	pipeline, _, _ := newTestPipeline(t, engine)
	req := &asu.BuildRequest{
		Version:     strptr("24.10.8"),
		VersionCode: strptr("rWRONG"),
		Target:      strptr("testtarget/testsubtarget"),
		Profile:     strptr("testprofile"),
	}
	req.Normalize()
	_, err := pipeline.Build(context.Background(), newJob(req.RequestHash(), req))
	if err == nil {
		t.Fatal("expected error for version code mismatch")
	}
}

func TestQueueProcessesJobAndCaches(t *testing.T) {
	engine := &fakeEngine{
		info:     fakeMakeInfo,
		manifest: "base-files - 1.0\nbusybox - 1.2\nvim - 1.0\n",
		copyOut:  map[string]string{"profiles.json": fakeProfilesJSON},
	}
	cfg := config.Default()
	cfg.PublicPath = t.TempDir()
	cfg.Workers = 1
	store, err := NewStore(cfg.PublicPath)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := NewPipeline(cfg, engine, store, staticResolver{}, testLogger())
	events := NewEventLog(filepath.Join(cfg.PublicPath, "events.json"))
	queue := NewQueue(cfg, pipeline, store, events, testLogger())
	defer queue.Close()

	req := &asu.BuildRequest{
		Version:  strptr("24.10.8"),
		Target:   strptr("testtarget/testsubtarget"),
		Profile:  strptr("testprofile"),
		Packages: []string{"vim"},
	}
	req.Normalize()

	job, created := queue.Enqueue(req)
	if !created {
		t.Fatal("expected a new job")
	}
	deadline := time.Now().Add(5 * time.Second)
	for job.State() != StateDone && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if job.State() != StateDone {
		t.Fatalf("job state = %s (stderr: %s)", job.State(), job.Stderr())
	}
	if queue.Len() != 0 {
		t.Fatalf("queue length = %d", queue.Len())
	}
	if got := events.Successes24h(); got != 1 {
		t.Fatalf("successes24h = %d", got)
	}

	// A second submission returns the cached job rather than a new build.
	same, createdAgain := queue.Enqueue(req)
	if createdAgain {
		t.Fatal("expected cached job, got a new one")
	}
	if same.ID != job.ID {
		t.Fatalf("cache returned different job: %s vs %s", same.ID, job.ID)
	}

	// The completed job round-trips through the response builder.
	payload, status := job.Response()
	if status != 200 || payload["detail"] != "done" {
		t.Fatalf("response = %v (%d)", payload, status)
	}

	// Persisted record can be reloaded (simulating a restart).
	if reloaded := store.LoadJob(job.ID); reloaded == nil || reloaded.State() != StateDone {
		t.Fatalf("persisted job not reloadable: %+v", reloaded)
	}
}
