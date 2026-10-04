// Package asu implements the Attended Sysupgrade (ASU) HTTP API contract.
//
// The request hashing, validation rules and JSON shapes in this package are
// deliberately byte-for-byte compatible with the upstream Python
// implementation (github.com/openwrt/asu) so that clients such as
// luci-app-attendedsysupgrade, owut and the OpenWrt firmware selector can talk
// to an OpenForge server without modification.
package asu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// BuildRequest mirrors asu.build_request.BuildRequest.
type BuildRequest struct {
	Distro           *string           `json:"distro,omitempty"`
	Version          *string           `json:"version"`
	VersionCode      *string           `json:"version_code,omitempty"`
	Target           *string           `json:"target"`
	Profile          *string           `json:"profile"`
	Packages         []string          `json:"packages"`
	PackagesVersions map[string]string `json:"packages_versions"`
	DiffPackages     bool              `json:"diff_packages"`
	Defaults         *string           `json:"defaults,omitempty"`
	RootfsSizeMB     *int              `json:"rootfs_size_mb,omitempty"`
	Filesystem       *string           `json:"filesystem,omitempty"`
	Repositories     map[string]string `json:"repositories"`
	RepositoriesMode string            `json:"repositories_mode"`
	RepositoryKeys   []string          `json:"repository_keys"`
	Client           *string           `json:"client,omitempty"`
}

// Normalize fills in the default values that ASU applies when decoding a
// request. It never mutates fields that were explicitly provided.
func (r *BuildRequest) Normalize() {
	if r.Distro == nil {
		d := "openwrt"
		r.Distro = &d
	}
	if r.VersionCode == nil {
		v := ""
		r.VersionCode = &v
	}
	if r.Packages == nil {
		r.Packages = []string{}
	}
	if r.PackagesVersions == nil {
		r.PackagesVersions = map[string]string{}
	}
	if r.Repositories == nil {
		r.Repositories = map[string]string{}
	}
	if r.RepositoryKeys == nil {
		r.RepositoryKeys = []string{}
	}
	if r.RepositoriesMode == "" {
		r.RepositoriesMode = "replace"
	}
	// Older LuCI clients send comma separated device names.
	if r.Profile != nil {
		p := sanitizeProfile(*r.Profile)
		r.Profile = &p
	}
}

func sanitizeProfile(p string) string {
	out := make([]rune, 0, len(p))
	for _, c := range p {
		if c == ',' {
			out = append(out, '_')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}

// distro returns the effective distro, applying the "openwrt" default.
func (r *BuildRequest) distro() string {
	if r.Distro == nil {
		return "openwrt"
	}
	return *r.Distro
}

func (r *BuildRequest) versionCode() string {
	if r.VersionCode == nil {
		return ""
	}
	return *r.VersionCode
}

// RequestHash reproduces asu.util.get_request_hash exactly.
func (r *BuildRequest) RequestHash() string {
	packages := r.Packages
	if len(r.PackagesVersions) > 0 {
		packages = make([]string, 0, len(r.PackagesVersions))
		for k := range r.PackagesVersions {
			packages = append(packages, k)
		}
	}
	return sha256Hex(concat(
		r.distro(),
		deref(r.Version),
		r.versionCode(),
		deref(r.Target),
		deref(r.Profile),
		packagesHash(packages),
		manifestHash(r.PackagesVersions),
		pythonBool(r.DiffPackages),
		deref(r.Filesystem),
		strHash(deref(r.Defaults)),
		pythonInt(r.RootfsSizeMB),
		pythonList(r.RepositoryKeys),
		pythonMap(r.Repositories),
		r.repositoriesMode(),
	))
}

func (r *BuildRequest) repositoriesMode() string {
	if r.RepositoriesMode == "" {
		return "replace"
	}
	return r.RepositoriesMode
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func strHash(s string) string { return sha256Hex(s) }

func packagesHash(packages []string) string {
	set := map[string]struct{}{}
	for _, p := range packages {
		set[trimPlus(p)] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return sha256Hex(joinSpace(keys))
}

// GetPackagesHash exposes the ASU package-list hash for the built-in builder.
func GetPackagesHash(packages []string) string { return packagesHash(packages) }

func manifestHash(manifest map[string]string) string {
	return sha256Hex(jsonDumps(manifest))
}

// jsonDumps mimics Python's json.dumps(manifest, sort_keys=True) which is used
// for the manifest hash. Values are always strings in an ASU manifest.
func jsonDumps(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := []byte{'{'}
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',', ' ')
		}
		buf = append(buf, jsonString(k)...)
		buf = append(buf, ':', ' ')
		buf = append(buf, jsonString(m[k])...)
	}
	buf = append(buf, '}')
	return string(buf)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// pythonBool renders a Go bool the way Python's str() would.
func pythonBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// pythonInt renders a nullable int the way Python's str() would ("None").
func pythonInt(v *int) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprintf("%d", *v)
}

// pythonList renders a []string the way Python's str() would, e.g. ['a', 'b'].
func pythonList(l []string) string {
	if len(l) == 0 {
		return "[]"
	}
	out := "["
	for i, s := range l {
		if i > 0 {
			out += ", "
		}
		out += "'" + s + "'"
	}
	return out + "]"
}

// pythonMap renders a map[string]string the way Python's str() would for the
// common empty case. Non-empty maps are rendered deterministically (sorted).
func pythonMap(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "{"
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += "'" + k + "': '" + m[k] + "'"
	}
	return out + "}"
}

func concat(parts ...string) string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	b := make([]byte, 0, n)
	for _, p := range parts {
		b = append(b, p...)
	}
	return string(b)
}

func joinSpace(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

func trimPlus(s string) string {
	if len(s) > 0 && s[0] == '+' {
		return s[1:]
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
