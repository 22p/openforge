package asu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openforge/openforge/internal/config"
)

// BackendResponse is a buffered upstream response.
type BackendResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Backend performs the actual image builds. OpenForge ships with a built-in
// builder (internal/builder) and a remote implementation that speaks the ASU
// API, so an existing ASU worker can be delegated to instead.
type Backend interface {
	Build(ctx context.Context, req *BuildRequest) (*BackendResponse, error)
	BuildStatus(ctx context.Context, hash string) (*BackendResponse, error)
	Store(ctx context.Context, path string) (*http.Response, error)
	Stats(ctx context.Context) (*BackendResponse, error)
	Endpoint() string
}

// StatsProvider is implemented by backends that serve build statistics
// themselves (the built-in builder) rather than proxying to a remote ASU.
type StatsProvider interface {
	StatsSummary() map[string]any
	BuildsPerDay() map[string]any
	BuildsByVersion() map[string]any
	TopPackages() map[string]any
	BuildErrors() string
}

// RemoteBackend proxies requests to an ASU compatible server.
type RemoteBackend struct {
	baseURL    string
	client     *http.Client
	storeClnt  *http.Client
	clientName string
}

// NewRemoteBackend creates a backend client for the given base URL.
func NewRemoteBackend(cfg *config.Config, clientName string) *RemoteBackend {
	base := strings.TrimRight(cfg.BackendURL, "/")
	return &RemoteBackend{
		baseURL:    base,
		client:     &http.Client{Timeout: cfg.HTTPTimeout},
		storeClnt:  &http.Client{Timeout: 5 * time.Minute},
		clientName: clientName,
	}
}

// Endpoint returns the backend base URL.
func (b *RemoteBackend) Endpoint() string { return b.baseURL }

// Build forwards a validated build request to the backend.
func (b *RemoteBackend) Build(ctx context.Context, req *BuildRequest) (*BackendResponse, error) {
	if req.Client == nil {
		name := b.clientName
		req.Client = &name
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return b.do(ctx, http.MethodPost, "/api/v1/build", bytes.NewReader(payload), "application/json")
}

// BuildStatus fetches the status of a previously submitted build.
func (b *RemoteBackend) BuildStatus(ctx context.Context, hash string) (*BackendResponse, error) {
	return b.do(ctx, http.MethodGet, "/api/v1/build/"+hash, nil, "")
}

// Stats fetches builder statistics from the backend.
func (b *RemoteBackend) Stats(ctx context.Context) (*BackendResponse, error) {
	return b.do(ctx, http.MethodGet, "/api/v1/stats", nil, "")
}

// Store streams an artifact from the backend, preserving redirects so that
// clients can download directly from object storage.
func (b *RemoteBackend) Store(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/store/"+path, nil)
	if err != nil {
		return nil, err
	}
	// Do not follow redirects: relay them to the browser so downloads can go
	// straight to S3/CDN without doubling the traffic through OpenForge.
	clnt := *b.storeClnt
	clnt.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return clnt.Do(req)
}

// ProxyStats forwards an arbitrary stats endpoint to the backend.
func (b *RemoteBackend) ProxyStats(ctx context.Context, subPath string) (*BackendResponse, error) {
	return b.do(ctx, http.MethodGet, "/api/v1"+subPath, nil, "")
}

func (b *RemoteBackend) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*BackendResponse, error) {
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "OpenForge")
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	return &BackendResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: data}, nil
}

// UnavailableBackend is used when no build backend is configured.
type UnavailableBackend struct{ reason string }

// NewUnavailableBackend returns a backend that reports a configuration error.
func NewUnavailableBackend(reason string) *UnavailableBackend {
	return &UnavailableBackend{reason: reason}
}

// Endpoint implements Backend.
func (u *UnavailableBackend) Endpoint() string { return "" }

func (u *UnavailableBackend) err() error { return fmt.Errorf("%s", u.reason) }

// Build implements Backend.
func (u *UnavailableBackend) Build(context.Context, *BuildRequest) (*BackendResponse, error) {
	return nil, u.err()
}

// BuildStatus implements Backend.
func (u *UnavailableBackend) BuildStatus(context.Context, string) (*BackendResponse, error) {
	return nil, u.err()
}

// Store implements Backend.
func (u *UnavailableBackend) Store(context.Context, string) (*http.Response, error) {
	return nil, u.err()
}

// Stats implements Backend.
func (u *UnavailableBackend) Stats(context.Context) (*BackendResponse, error) { return nil, u.err() }
