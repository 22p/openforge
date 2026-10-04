package builder

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/openforge/openforge/internal/asu"
)

// This file ports the pure helpers from asu/util.py and asu/package_changes.py.

var releaseVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-rc\d+)?$`)

// ContainerVersionTag mirrors asu.util.get_container_version_tag.
func ContainerVersionTag(version string) string {
	if releaseVersionRe.MatchString(version) {
		return "v" + version
	}
	if version == "SNAPSHOT" {
		return "master"
	}
	return "openwrt-" + strings.TrimSuffix(version, "-SNAPSHOT")
}

// IsSnapshotBuild reports whether the version uses a setup.sh based
// ImageBuilder (asu.util.is_snapshot_build).
func IsSnapshotBuild(version string) bool {
	return strings.HasSuffix(strings.ToLower(version), "snapshot")
}

// ImageName builds the ImageBuilder container reference for a target/version.
func ImageName(baseContainer, target, version string) string {
	return fmt.Sprintf("%s:%s-%s", baseContainer, strings.ReplaceAll(target, "/", "-"), ContainerVersionTag(version))
}

// DiffPackages reproduces asu.util.diff_packages: return the list of packages
// to remove ("-pkg") followed by the requested packages, in order.
func DiffPackages(requested, defaults []string) []string {
	defaultSet := map[string]bool{}
	for _, p := range defaults {
		defaultSet[p] = true
	}
	requestedSet := map[string]bool{}
	for _, p := range requested {
		requestedSet[p] = true
	}
	remove := []string{}
	for p := range defaultSet {
		if !requestedSet[p] {
			remove = append(remove, strings.ReplaceAll("-"+p, "--", "-"))
		}
	}
	sort.Strings(remove)
	return append(remove, requested...)
}

// ParseManifest parses an OpenWrt manifest (opkg "name - version" or apk
// "name version").
func ParseManifest(content string) map[string]string {
	separator := " "
	if strings.Contains(content, " - ") {
		separator = " - "
	}
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, separator, 2)
		if len(parts) == 2 {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return out
}

// CheckManifest verifies that requested package versions are present in the
// built manifest (asu.util.check_manifest). Returns "" when valid.
func CheckManifest(manifest, packagesVersions map[string]string) string {
	// Iterate deterministically so error messages are reproducible.
	names := make([]string, 0, len(packagesVersions))
	for name := range packagesVersions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		version := packagesVersions[name]
		found, ok := manifest[name]
		if !ok {
			return fmt.Sprintf("Impossible package selection: %s not in manifest", name)
		}
		if version != found {
			return fmt.Sprintf("Impossible package selection: %s version not as requested: %s vs. %s", name, version, found)
		}
	}
	return ""
}

var (
	reMissingOpkg  = regexp.MustCompile(`Cannot install package ([^ ]+)\.`)
	reMissingApk   = regexp.MustCompile(` ([^ ]+) \(no such package\)`)
	reApkConflicts = regexp.MustCompile(`(?s)\n +([^:\n]+):\n +conflicts: ([^[]+)`)
	reOpkgClash    = regexp.MustCompile(`check_data_file_clashes: Package ([^ ]+) wants to`)
	reAlreadyProv  = regexp.MustCompile(`(?m)is already provided by package  \* ([^ ]+)$`)
	reConflictFor  = regexp.MustCompile(`(?m)\* check_conflicts_for:.+ ([^ ]+)(?: \*|:)$`)
	reApkOverwrite = regexp.MustCompile(`ERROR: ([^ ]+): trying to overwrite`)
	reOwnedBy      = regexp.MustCompile(`trying to overwrite .* owned by ([^ ]+)\.`)
	reWgetFail     = regexp.MustCompile(`(?s)ERROR: wget: exited with error \d+\nERROR: ([^:]+)`)
)

// CheckPackageErrors extracts a human readable summary from an ImageBuilder
// stderr log (asu.util.check_package_errors).
func CheckPackageErrors(stderr string) string {
	missing := map[string]bool{}
	for _, m := range reMissingOpkg.FindAllStringSubmatch(stderr, -1) {
		missing[m[1]] = true
	}
	for _, m := range reMissingApk.FindAllStringSubmatch(stderr, -1) {
		missing[m[1]] = true
	}

	conflicts := map[string]bool{}
	for _, m := range reApkConflicts.FindAllStringSubmatch(stderr, -1) {
		conflicts[m[1]] = true
		conflicts[m[2]] = true
	}
	for _, re := range []*regexp.Regexp{reOpkgClash, reAlreadyProv, reConflictFor, reApkOverwrite, reOwnedBy} {
		for _, m := range re.FindAllStringSubmatch(stderr, -1) {
			conflicts[m[1]] = true
		}
	}

	downloads := map[string]bool{}
	for _, m := range reWgetFail.FindAllStringSubmatch(stderr, -1) {
		downloads[m[1]] = true
	}

	for pkg := range conflicts {
		delete(missing, pkg)
	}

	suffix := ""
	if len(missing) > 0 || len(conflicts) > 0 || len(downloads) > 0 {
		suffix = ":"
	}
	if len(missing) > 0 {
		suffix += " missing (" + strings.Join(sortedKeys(missing), ", ") + ")"
	}
	if len(conflicts) > 0 {
		suffix += " conflicts (" + strings.Join(sortedKeys(conflicts), ", ") + ")"
	}
	if len(downloads) > 0 {
		suffix += " wget-fails (" + strings.Join(sortedKeys(downloads), ", ") + ")"
	}
	return "Impossible package selection" + suffix
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FingerprintPubkeyUsign returns the short fingerprint of a usign public key,
// matching asu.util.fingerprint_pubkey_usign.
func FingerprintPubkeyUsign(pubkey string) (string, error) {
	lines := strings.Split(strings.TrimSpace(pubkey), "\n")
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[len(lines)-1]))
	if err != nil {
		return "", err
	}
	if len(decoded) < 10 {
		return "", fmt.Errorf("public key too short")
	}
	return hex.EncodeToString(decoded[2:10]), nil
}

// MergeRepositories reproduces asu.repositories.merge_repositories.
func MergeRepositories(base string, extra map[string]string, apkMode bool) string {
	lines := []string{}
	for _, line := range strings.Split(base, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if apkMode {
			lines = append(lines, extra[name])
		} else {
			lines = append(lines, fmt.Sprintf("src/gz %s %s", name, extra[name]))
		}
	}
	if !apkMode {
		hasImagebuilder := false
		hasSignature := false
		for _, line := range lines {
			if strings.Contains(line, "src imagebuilder file:packages") {
				hasImagebuilder = true
			}
			if strings.Contains(line, "option check_signature") {
				hasSignature = true
			}
		}
		if !hasImagebuilder {
			lines = append(lines, "src imagebuilder file:packages")
		}
		if !hasSignature {
			lines = append(lines, "option check_signature")
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// ApplyPackageChanges ports asu.package_changes.apply_package_changes. It
// adjusts the requested package list for known upstream renames and fixes.
func ApplyPackageChanges(req *asu.BuildRequest) {
	if req.Version == nil || req.Profile == nil || req.Target == nil {
		return
	}
	version := *req.Version
	target := *req.Target
	profile := *req.Profile

	add := func(pkg string) {
		for _, p := range req.Packages {
			if p == pkg {
				return
			}
		}
		req.Packages = append(req.Packages, pkg)
	}
	remove := func(pkg string) bool {
		for i, p := range req.Packages {
			if p == pkg {
				req.Packages = append(req.Packages[:i], req.Packages[i+1:]...)
				return true
			}
		}
		return false
	}
	inSet := func(set map[string]bool) bool { return set[profile] }

	if strings.HasPrefix(version, "23.05") {
		switch target {
		case "mediatek/mt7622":
			add("kmod-mt7622-firmware")
		case "ath79/generic":
			if inSet(map[string]bool{
				"buffalo_wzr-hp-g300nh-s": true, "dlink_dir-825-b1": true,
				"netgear_wndr3700": true, "netgear_wndr3700-v2": true,
				"netgear_wndr3800": true, "netgear_wndr3800ch": true,
				"netgear_wndrmac-v1": true, "netgear_wndrmac-v2": true,
				"trendnet_tew-673gru": true,
			}) {
				add("kmod-switch-rtl8366s")
			} else if profile == "buffalo_wzr-hp-g300nh-rb" {
				add("kmod-switch-rtl8366rb")
			}
		}
	}

	if strings.HasPrefix(version, "24.10") {
		if remove("auc") {
			add("owut")
		}
		if profile == "tplink_archer-c6-v2" {
			add("ipq-wifi-tplink_archer-c6-v2")
		}
		switch target {
		case "mediatek/filogic", "mediatek/mt7622", "mediatek/mt7623":
			add("fitblk")
		}
	}

	if strings.HasPrefix(version, "25.12") {
		switch target {
		case "kirkwood/generic":
			if inSet(map[string]bool{
				"checkpoint_l-50": true, "endian_4i-edge-200": true,
				"linksys_e4200-v2": true, "linksys_ea3500": true, "linksys_ea4500": true,
			}) {
				add("kmod-dsa-mv88e6xxx")
			}
		case "mvebu/cortexa9":
			if inSet(map[string]bool{
				"cznic_turris-omnia": true, "fortinet_fg-30e": true, "fortinet_fwf-30e": true,
				"fortinet_fg-50e": true, "fortinet_fg-51e": true, "fortinet_fg-52e": true,
				"fortinet_fwf-50e-2r": true, "fortinet_fwf-51e": true, "iij_sa-w2": true,
				"linksys_wrt1200ac": true, "linksys_wrt1900acs": true, "linksys_wrt1900ac-v1": true,
				"linksys_wrt1900ac-v2": true, "linksys_wrt3200acm": true, "linksys_wrt32x": true,
				"marvell_a370-rd": true,
			}) {
				add("kmod-dsa-mv88e6xxx")
			}
		case "mvebu/cortexa53":
			if inSet(map[string]bool{
				"glinet_gl-mv1000": true, "globalscale_espressobin": true,
				"globalscale_espressobin-emmc": true, "globalscale_espressobin-ultra": true,
				"globalscale_espressobin-v7": true, "globalscale_espressobin-v7-emmc": true,
				"methode_udpu": true,
			}) {
				add("kmod-dsa-mv88e6xxx")
			}
		case "mvebu/cortexa72":
			if inSet(map[string]bool{
				"checkpoint_v-80": true, "checkpoint_v-81": true, "globalscale_mochabin": true,
				"mikrotik_rb5009": true, "solidrun_clearfog-pro": true,
			}) {
				add("kmod-dsa-mv88e6xxx")
			}
		case "lantiq/xrx200":
			add("kmod-dsa-gswip")
			if inSet(map[string]bool{
				"arcadyan_arv7519rw22": true, "arcadyan_vgv7510kw22-brn": true,
				"arcadyan_vgv7510kw22-nor": true, "avm_fritz7412": true, "avm_fritz7430": true,
				"buffalo_wbmr-300hpd": true,
			}) {
				add("xrx200-rev1.1-phy22f-firmware")
				add("xrx200-rev1.2-phy22f-firmware")
			} else if inSet(map[string]bool{
				"tplink_vr200": true, "tplink_vr200v": true, "arcadyan_vgv7519-brn": true,
				"arcadyan_vgv7519-nor": true, "arcadyan_vrv9510kwac23": true,
				"avm_fritz3370-rev2-hynix": true, "avm_fritz3370-rev2-micron": true,
				"avm_fritz3390": true, "avm_fritz3490": true, "avm_fritz3490-micron": true,
				"avm_fritz5490": true, "avm_fritz5490-micron": true, "avm_fritz7360sl": true,
				"avm_fritz7360-v2": true, "avm_fritz7362sl": true, "avm_fritz7490": true,
				"avm_fritz7490-micron": true, "bt_homehub-v5a": true,
				"lantiq_easy80920-nand": true, "lantiq_easy80920-nor": true,
				"zyxel_p-2812hnu-f1": true, "zyxel_p-2812hnu-f3": true,
			}) {
				add("xrx200-rev1.1-phy11g-firmware")
				add("xrx200-rev1.2-phy11g-firmware")
			}
		case "lantiq/xrx200_legacy":
			add("kmod-dsa-gswip")
			if inSet(map[string]bool{"alphanetworks_asl56026": true, "netgear_dm200": true}) {
				add("xrx200-rev1.1-phy22f-firmware")
				add("xrx200-rev1.2-phy22f-firmware")
			} else if inSet(map[string]bool{
				"tplink_tdw8970": true, "tplink_tdw8980": true, "arcadyan_vg3503j": true,
			}) {
				add("xrx200-rev1.1-phy11g-firmware")
				add("xrx200-rev1.2-phy11g-firmware")
			}
		case "bcm53xx/generic":
			if profile == "meraki_mr32" {
				add("kmod-hci-uart")
			}
		case "ipq40xx/generic":
			if inSet(map[string]bool{"linksys_whw03": true, "linksys_whw03v2": true}) {
				add("kmod-hci-uart")
			}
		case "qualcommax/ipq807x":
			if inSet(map[string]bool{
				"linksys_mx4200v1": true, "linksys_mx8500": true, "zyxel_nbg7815": true,
			}) {
				add("kmod-hci-uart")
			}
		case "ath79/mikrotik":
			add("kmod-ag71xx-legacy")
		}
	}

	if version == "SNAPSHOT" {
		remove("kmod-nf-conntrack6")
		remove("kmod-lib-crc32c")
	}

	// Language pack replacements apply to this version and newer.
	languagePacks := []struct {
		version string
		renames map[string]string
	}{
		{"24.10", map[string]string{"luci-i18n-opkg-": "luci-i18n-package-manager-"}},
	}
	for _, lp := range languagePacks {
		if version >= lp.version {
			for i, pkg := range req.Packages {
				for old, renamed := range lp.renames {
					if strings.HasPrefix(pkg, old) {
						req.Packages[i] = renamed + strings.TrimPrefix(pkg, old)
					}
				}
			}
		}
	}
	for _, pkg := range append([]string(nil), req.Packages...) {
		if strings.HasPrefix(pkg, "luci-i18n-") && strings.HasSuffix(pkg, "-en") {
			remove(pkg)
		}
	}
}
