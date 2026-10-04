package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openforge/openforge/internal/config"
	"github.com/openforge/openforge/internal/server"
)

const profilesJSON = `{
  "version_code": "r12647-cb44ab4f5d",
  "source_date_epoch": "1612136917",
  "arch_packages": "testarch",
  "default_packages": ["base-files", "busybox"],
  "profiles": {
    "testprofile": {
      "supported_devices": ["Test Device"],
      "device_packages": ["kmod-test"],
      "images": [
        {"name": "openwrt-testtarget-squashfs-sysupgrade.bin", "sha256": "abc123", "type": "sysupgrade", "filesystem": "squashfs"}
      ]
    }
  }
}`

// fakeUpstream serves the minimal set of OpenWrt download metadata used by the
// metadata service.
func fakeUpstream() *httptest.Server {
	mux := http.NewServeMux()
	write := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}
	}
	mux.HandleFunc("/.versions.json", write(`{"stable_version":"TEST.1","oldstable_version":"","upcoming_version":"","versions_list":["TEST.1"]}`))
	mux.HandleFunc("/releases/TEST.1/.targets.json", write(`{"testtarget/testsubtarget":"testarch"}`))
	mux.HandleFunc("/releases/TEST.1/targets/testtarget/testsubtarget/profiles.json", write(profilesJSON))
	mux.HandleFunc("/releases/TEST.1/targets/testtarget/testsubtarget/packages/index.json", write(`{"version":2,"architecture":"testarch","packages":{"vim":"1.0","tmux":"2.0"}}`))
	return httptest.NewServer(mux)
}

func fakeBackend(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/build", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("X-Imagebuilder-Status", "done")
			w.Header().Set("X-Queue-Position", "0")
			_, _ = io.WriteString(w, `{"status":200,"detail":"done","imagebuilder_status":"done","request_hash":"deadbeef","bin_dir":"deadbeef","images":[{"name":"openwrt-testtarget-squashfs-sysupgrade.bin","sha256":"abc123","type":"sysupgrade"}],"manifest":{"base-files":"1.0"}}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["target"] == "" {
			t.Error("backend did not receive a target")
		}
		w.Header().Set("X-Imagebuilder-Status", "done")
		w.Header().Set("X-Queue-Position", "0")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":200,"detail":"done","imagebuilder_status":"done","request_hash":"deadbeef","bin_dir":"deadbeef","images":[{"name":"openwrt-testtarget-squashfs-sysupgrade.bin","sha256":"abc123","type":"sysupgrade"}],"manifest":{"base-files":"1.0"}}`)
	})
	mux.HandleFunc("/api/v1/build/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Imagebuilder-Status", "done")
		w.Header().Set("X-Queue-Position", "0")
		_, _ = io.WriteString(w, `{"status":200,"detail":"done","request_hash":"deadbeef","bin_dir":"deadbeef"}`)
	})
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"queue_length":7}`)
	})
	mux.HandleFunc("/store/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "FIRMWARE-BYTES")
	})
	return httptest.NewServer(mux)
}

func newTestServer(t *testing.T) (*httptest.Server, *config.Config) {
	t.Helper()
	upstream := fakeUpstream()
	backend := fakeBackend(t)
	t.Cleanup(upstream.Close)
	t.Cleanup(backend.Close)

	cfg := config.Default()
	cfg.UpstreamURL = upstream.URL
	cfg.BackendURL = backend.URL
	cfg.Branches = map[string]config.Branch{
		"TEST": {Path: "releases/{version}", Enabled: true},
	}
	cfg.AllowDefaults = true

	srv, err := server.New(cfg, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, cfg
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustGetClient(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestJSONOverview(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/json/v1/overview.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var doc struct {
		Latest   []string `json:"latest"`
		Branches map[string]struct {
			Versions []string `json:"versions"`
			Targets  map[string]string
		} `json:"branches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Latest) != 1 || doc.Latest[0] != "TEST.1" {
		t.Fatalf("latest = %v", doc.Latest)
	}
	if doc.Branches["TEST"].Targets["testtarget/testsubtarget"] != "testarch" {
		t.Fatalf("branch targets missing: %+v", doc.Branches["TEST"])
	}
}

func TestRevision(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := mustGet(t, ts.URL+"/api/v1/revision/TEST.1/testtarget/testsubtarget")
	var doc map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if doc["revision"] != "r12647-cb44ab4f5d" {
		t.Fatalf("revision = %q", doc["revision"])
	}
}

func TestBuildSuccessRelay(t *testing.T) {
	ts, _ := newTestServer(t)
	body := `{"version":"TEST.1","target":"testtarget/testsubtarget","profile":"testprofile","packages":["vim"],"diff_packages":true}`
	resp, err := http.Post(ts.URL+"/api/v1/build", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Imagebuilder-Status"); got != "done" {
		t.Fatalf("imagebuilder status header = %q", got)
	}
	var doc struct {
		Status   int    `json:"status"`
		Detail   string `json:"detail"`
		BinDir   string `json:"bin_dir"`
		RequestH string `json:"request_hash"`
		Images   []struct {
			Name string `json:"name"`
		} `json:"images"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Detail != "done" || doc.BinDir != "deadbeef" || len(doc.Images) != 1 {
		t.Fatalf("unexpected build response: %+v", doc)
	}
}

func TestBuildValidationFailure(t *testing.T) {
	ts, _ := newTestServer(t)
	body := `{"version":"TEST.1","target":"testtarget/testsubtarget","profile":"nope"}`
	resp := mustPost(t, ts.URL+"/api/v1/build", body)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	detail, _ := doc["detail"].(string)
	if !strings.HasPrefix(detail, "Unsupported profile: nope") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestPackageIndex(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := mustGet(t, ts.URL+"/json/v1/releases/TEST.1/targets/testtarget/testsubtarget/index.json")
	defer resp.Body.Close()
	var doc struct {
		Architecture string            `json:"architecture"`
		Packages     map[string]string `json:"packages"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if doc.Architecture != "testarch" || doc.Packages["vim"] != "1.0" {
		t.Fatalf("unexpected package index: %+v", doc)
	}
}

func TestProfilesPassthrough(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := mustGet(t, ts.URL+"/json/v1/releases/TEST.1/targets/testtarget/testsubtarget/profiles.json")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var doc struct {
		DefaultPackages []string `json:"default_packages"`
		Profiles        map[string]any
	}
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if len(doc.DefaultPackages) != 2 || doc.Profiles["testprofile"] == nil {
		t.Fatalf("unexpected profiles passthrough: %+v", doc)
	}
}

func TestRedirects(t *testing.T) {
	ts, _ := newTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, path := range []string{"/api/v1/latest", "/api/v1/overview"} {
		resp := mustGetClient(t, client, ts.URL+path)
		if resp.StatusCode != 301 {
			t.Fatalf("%s status = %d, want 301", path, resp.StatusCode)
		}
	}
}

func TestStorePassthrough(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := mustGet(t, ts.URL+"/store/deadbeef/openwrt.bin")
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(raw) != "FIRMWARE-BYTES" {
		t.Fatalf("store status=%d body=%q", resp.StatusCode, raw)
	}
}

func TestStats(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := mustGet(t, ts.URL+"/api/v1/stats")
	defer resp.Body.Close()
	var doc struct {
		QueueLength int `json:"queue_length"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if doc.QueueLength != 7 {
		t.Fatalf("queue_length = %d, want 7", doc.QueueLength)
	}
}

func TestBuildBoundsValidation(t *testing.T) {
	ts, cfg := newTestServer(t)
	cases := []struct {
		name string
		body string
	}{
		{"rootfs too small", `{"version":"TEST.1","target":"testtarget/testsubtarget","profile":"testprofile","rootfs_size_mb":0}`},
		{"rootfs too big", fmt.Sprintf(`{"version":"TEST.1","target":"testtarget/testsubtarget","profile":"testprofile","rootfs_size_mb":%d}`, cfg.MaxCustomRootfsSizeMB+1)},
		{"bad filesystem", `{"version":"TEST.1","target":"testtarget/testsubtarget","profile":"testprofile","filesystem":"bogus"}`},
		{"defaults too long", fmt.Sprintf(`{"version":"TEST.1","target":"testtarget/testsubtarget","profile":"testprofile","defaults":"%s"}`, strings.Repeat("#", cfg.MaxDefaultsLength+1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := mustPost(t, ts.URL+"/api/v1/build", tc.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", resp.StatusCode)
			}
		})
	}
}

func TestRequestHashRelayedForQueuedJob(t *testing.T) {
	// A queued job (202) must expose a request hash so clients can poll.
	upstream := fakeUpstream()
	t.Cleanup(upstream.Close)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Imagebuilder-Status", "queued")
		w.Header().Set("X-Queue-Position", "3")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":202,"detail":"queued","queue_position":3,"imagebuilder_status":"queued"}`)
	}))
	t.Cleanup(backend.Close)

	cfg := config.Default()
	cfg.UpstreamURL = upstream.URL
	cfg.BackendURL = backend.URL
	cfg.Branches = map[string]config.Branch{"TEST": {Path: "releases/{version}", Enabled: true}}
	srv, _ := server.New(cfg, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := `{"version":"TEST.1","target":"testtarget/testsubtarget","profile":"testprofile"}`
	resp := mustPost(t, ts.URL+"/api/v1/build", body)
	defer resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if doc["request_hash"] == nil || doc["request_hash"] == "" {
		t.Fatalf("missing request_hash in %+v", doc)
	}
	if resp.Header.Get("X-Queue-Position") != "3" {
		t.Fatalf("queue position header = %q", resp.Header.Get("X-Queue-Position"))
	}
}
