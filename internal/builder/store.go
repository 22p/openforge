package builder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store is a local filesystem artifact store. Generated images and job records
// live under <public_path>/store/<request_hash>/.
type Store struct {
	root string
}

// NewStore creates the store directory if needed and returns it.
func NewStore(publicPath string) (*Store, error) {
	root, err := filepath.Abs(filepath.Join(publicPath, "store"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// Dir returns the artifact directory for a request hash.
func (s *Store) Dir(hash string) string { return filepath.Join(s.root, hash) }

// SaveJob writes the job record next to its artifacts.
func (s *Store) SaveJob(job *Job) error {
	dir := s.Dir(job.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := job.marshalRecord()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "job.json"), data, 0o644)
}

// LoadJob reads a persisted job record, returning nil when absent or invalid.
func (s *Store) LoadJob(hash string) *Job {
	if !validHash(hash) {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(hash), "job.json"))
	if err != nil {
		return nil
	}
	var record persisted
	if err := json.Unmarshal(data, &record); err != nil {
		return nil
	}
	if record.Request == nil {
		return nil
	}
	return jobFromPersisted(record)
}

// RemoveJob deletes a persisted job record (artifacts are kept).
func (s *Store) RemoveJob(hash string) {
	if !validHash(hash) {
		return
	}
	_ = os.Remove(filepath.Join(s.Dir(hash), "job.json"))
}

// Open safely resolves a store-relative path for download.
func (s *Store) Open(rel string) (*os.File, int64, string, error) {
	clean := filepath.Clean("/" + strings.TrimPrefix(rel, "/"))
	full := filepath.Join(s.root, clean)
	if full != s.root && !strings.HasPrefix(full, s.root+string(os.PathSeparator)) {
		return nil, 0, "", fmt.Errorf("invalid path")
	}
	file, err := os.Open(full)
	if err != nil {
		return nil, 0, "", err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, "", err
	}
	if info.IsDir() {
		_ = file.Close()
		return nil, 0, "", fmt.Errorf("is a directory")
	}
	return file, info.Size(), info.Name(), nil
}

// validHash guards against path traversal in hash-derived paths.
func validHash(hash string) bool {
	if len(hash) == 0 || len(hash) > 128 {
		return false
	}
	for _, c := range hash {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}
