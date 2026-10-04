package asu

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ValidationError is a client error produced while validating a build request.
type ValidationError struct {
	Status int
	Detail string
}

func (e *ValidationError) Error() string { return e.Detail }

// Validate reproduces asu/routers/api.py:validate_request. On success the
// request is normalised in place: package lists are stripped of "+" prefixes
// and the profile is mapped to its canonical ImageBuilder name.
func (m *Metadata) Validate(ctx context.Context, req *BuildRequest) *ValidationError {
	req.Normalize()

	if err := m.validateModelBounds(req); err != nil {
		return err
	}

	if req.Defaults != nil && *req.Defaults != "" && !m.cfg.AllowDefaults {
		return &ValidationError{400, "Handling `defaults` not enabled on server"}
	}

	for _, repoURL := range req.Repositories {
		if !IsRepoAllowed(repoURL, m.cfg.RepositoryAllowList) {
			return &ValidationError{400, "Repository not allowed: " + repoURL}
		}
	}

	if req.distro() != "openwrt" {
		return &ValidationError{400, "Unsupported distro: " + req.distro()}
	}

	version := deref(req.Version)
	branch := m.BranchName(version)
	if _, ok := m.cfg.Branches[branch]; !ok {
		return &ValidationError{400, "Unsupported branch: " + version}
	}

	if !containsString(m.Versions(ctx), version) {
		return &ValidationError{400, "Unsupported version: " + version}
	}

	// Normalise the package selection: packages_versions takes precedence and
	// the leading "+" (keep default) marker is stripped.
	if len(req.PackagesVersions) > 0 {
		names := make([]string, 0, len(req.PackagesVersions))
		for name := range req.PackagesVersions {
			names = append(names, name)
		}
		sort.Strings(names)
		normalized := make([]string, 0, len(names))
		for _, name := range names {
			normalized = append(normalized, trimPlus(name))
		}
		req.Packages = normalized
	} else {
		normalized := make([]string, 0, len(req.Packages))
		for _, name := range req.Packages {
			normalized = append(normalized, trimPlus(name))
		}
		req.Packages = normalized
	}

	target := deref(req.Target)
	targets, err := m.Targets(ctx, version)
	if err != nil || targets[target] == "" {
		// The target may have just been published; bypass the cache once.
		if refreshed, rerr := m.RefreshTargets(ctx, version); rerr == nil {
			targets = refreshed
		}
		if targets[target] == "" {
			return &ValidationError{400, fmt.Sprintf(
				"Unsupported target: %s. The requested target was either dropped, "+
					"is still being built or is not supported by the selected version. "+
					"Please check the forums or try again later.", target)}
		}
	}

	profile := deref(req.Profile)
	profiles, err := m.Profiles(ctx, version, target)
	if err != nil || !validProfile(profiles, profile) {
		if refreshed, rerr := m.RefreshProfiles(ctx, version, target); rerr == nil {
			profiles = refreshed
		}
		if len(profiles) == 1 {
			if _, ok := profiles["generic"]; ok {
				g := "generic"
				req.Profile = &g
				return nil
			}
		}
		if !validProfile(profiles, profile) {
			return &ValidationError{400, fmt.Sprintf(
				"Unsupported profile: %s. The requested profile was either dropped "+
					"or never existed. Please check the forums for more information.", profile)}
		}
	}

	canonical := profiles[profile]
	req.Profile = &canonical
	return nil
}

func validProfile(profiles map[string]string, profile string) bool {
	_, ok := profiles[profile]
	return ok
}

// filesystems mirrors the Literal accepted by ASU's BuildRequest model.
var filesystems = map[string]bool{
	"squashfs": true, "ext4": true, "ubifs": true, "erofs": true,
	"jffs2": true, "jffs2-nand": true, "targz": true, "cpiogz": true,
}

// validateModelBounds reproduces the pydantic field constraints in
// asu/build_request.py. Violations are 422 like FastAPI.
func (m *Metadata) validateModelBounds(req *BuildRequest) *ValidationError {
	if req.RootfsSizeMB != nil {
		if *req.RootfsSizeMB < 1 {
			return &ValidationError{422, "Input should be greater than or equal to 1"}
		}
		if max := m.cfg.MaxCustomRootfsSizeMB; *req.RootfsSizeMB > max {
			return &ValidationError{422, fmt.Sprintf("Input should be less than or equal to %d", max)}
		}
	}
	if req.Defaults != nil && len(*req.Defaults) > m.cfg.MaxDefaultsLength {
		return &ValidationError{422, fmt.Sprintf(
			"String should have at most %d characters", m.cfg.MaxDefaultsLength)}
	}
	if req.Filesystem != nil && !filesystems[*req.Filesystem] {
		return &ValidationError{422, fmt.Sprintf(
			"Input should be one of: squashfs, ext4, ubifs, erofs, jffs2, jffs2-nand, targz, cpiogz")}
	}
	if req.RepositoriesMode != "append" && req.RepositoriesMode != "replace" {
		return &ValidationError{422, "Input should be 'append' or 'replace'"}
	}
	return nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// IsRepoAllowed behaves like asu.repositories.is_repo_allowed: a URL is allowed
// only if it shares scheme and host with an allow-list entry and its path is
// nested below the entry path.
func IsRepoAllowed(repoURL string, allowList []string) bool {
	if len(allowList) == 0 {
		return false
	}
	parsed, err := url.Parse(repoURL)
	if err != nil {
		return false
	}
	for _, allowed := range allowList {
		allowedParsed, err := url.Parse(allowed)
		if err != nil {
			continue
		}
		if parsed.Scheme == allowedParsed.Scheme &&
			parsed.Hostname() == allowedParsed.Hostname() &&
			strings.HasPrefix(parsed.Path, strings.TrimRight(allowedParsed.Path, "/")+"/") {
			return true
		}
	}
	return false
}
