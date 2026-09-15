package release

import (
	"path"
	"path/filepath"
	"strings"
)

const (
	PublicToolsDirectory       = "Tools"
	LegacyReleaseDirectoryName = "releases"
)

// PublishedPath is the canonical public location for every product release.
// Product files stay inside the site's existing /Tools namespace, while the
// releases component distinguishes immutable signed assets from manual files.
func PublishedPath(product, channel, version string) string {
	return filepath.ToSlash(filepath.Join(
		PublicToolsDirectory,
		product,
		channel,
		version,
	))
}

func legacyPublishedPath(product, channel, version string) string {
	return filepath.ToSlash(filepath.Join(LegacyReleaseDirectoryName, product, channel, version))
}

// IsManagedPublishedPath reports whether a public path belongs to either the
// canonical immutable release tree or the retired root-level release tree.
func IsManagedPublishedPath(relative string) bool {
	normalized := strings.TrimPrefix(path.Clean("/"+strings.Trim(strings.ReplaceAll(relative, "\\", "/"), "/")), "/")
	if normalized == LegacyReleaseDirectoryName || strings.HasPrefix(normalized, LegacyReleaseDirectoryName+"/") {
		return true
	}
	parts := strings.Split(normalized, "/")
	return len(parts) >= 3 &&
		parts[0] == PublicToolsDirectory &&
		identifierPattern.MatchString(parts[1]) &&
		isReleaseChannel(parts[2])
}

// LegacyRedirectPath maps an old public URL onto the canonical Tools layout.
// The old namespace remains only as an HTTP compatibility entry point; it no
// longer stores release files.
func LegacyRedirectPath(requestPath string) (string, bool) {
	cleaned := path.Clean("/" + strings.TrimPrefix(requestPath, "/"))
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	if len(parts) < 2 || parts[0] != LegacyReleaseDirectoryName || !identifierPattern.MatchString(parts[1]) {
		return "", false
	}
	destination := []string{PublicToolsDirectory, parts[1]}
	destination = append(destination, parts[2:]...)
	redirect := "/" + path.Join(destination...)
	if strings.HasSuffix(requestPath, "/") && !strings.HasSuffix(redirect, "/") {
		redirect += "/"
	}
	return redirect, true
}

func isReleaseChannel(value string) bool {
	return value == "stable" || value == "beta" || value == "nightly"
}
