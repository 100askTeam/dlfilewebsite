package release

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"aead.dev/minisign"
)

const (
	ManifestName      = "release-set.json"
	MaxManifestBytes  = 1 << 20
	MaxReleaseAssets  = 128
	MaxSignatureBytes = 16 << 10
)

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	filePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,199}$`)
	versionPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z][0-9A-Za-z.-]{0,63}))?$`)
)

type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	Product       string  `json:"product"`
	Channel       string  `json:"channel"`
	Version       string  `json:"version"`
	PublishedAt   string  `json:"published_at"`
	Notes         string  `json:"notes"`
	Assets        []Asset `json:"assets"`
}

type Asset struct {
	Target      string   `json:"target"`
	Kind        string   `json:"kind"`
	FromVersion string   `json:"from_version,omitempty"`
	File        string   `json:"file"`
	Size        int64    `json:"size"`
	SHA256      string   `json:"sha256"`
	Signature   string   `json:"signature"`
	Mirrors     []string `json:"mirrors,omitempty"`
}

type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string
}

func ParseVersion(value string) (Version, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	match := versionPattern.FindStringSubmatch(value)
	if match == nil {
		return Version{}, fmt.Errorf("invalid semantic version %q", value)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	return Version{Major: major, Minor: minor, Patch: patch, Pre: match[4]}, nil
}

func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if v.Pre == other.Pre {
		return 0
	}
	if v.Pre == "" {
		return 1
	}
	if other.Pre == "" {
		return -1
	}
	return strings.Compare(v.Pre, other.Pre)
}

func DeltaCompatible(from, to Version) bool {
	if from.Compare(to) >= 0 || from.Major != to.Major {
		return false
	}
	if to.Major == 0 {
		return from.Minor == to.Minor
	}
	return true
}

func DecodePublicKey(value string) (minisign.PublicKey, error) {
	text := []byte(strings.TrimSpace(value))
	if len(text) == 0 {
		return minisign.PublicKey{}, errors.New("release public key is not configured")
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(text)); err == nil && strings.Contains(string(decoded), "minisign public key") {
		text = decoded
	}
	var key minisign.PublicKey
	if err := key.UnmarshalText(text); err != nil {
		return minisign.PublicKey{}, fmt.Errorf("decode release public key: %w", err)
	}
	return key, nil
}

func LoadAndVerify(directory string, publicKey minisign.PublicKey) (Manifest, error) {
	return loadAndVerify(directory, func(string) (minisign.PublicKey, error) {
		return publicKey, nil
	})
}

func LoadAndVerifyWithKeyring(directory string, keyring *PublicKeyring) (Manifest, error) {
	return loadAndVerify(directory, keyring.KeyFor)
}

func loadAndVerify(directory string, resolveKey func(string) (minisign.PublicKey, error)) (Manifest, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return Manifest{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve release directory: %w", err)
	}
	manifestPath := filepath.Join(root, ManifestName)
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > MaxManifestBytes {
		return Manifest{}, errors.New("release-set.json is absent, unsafe, or too large")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	publicKey, err := resolveKey(manifest.Product)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateDirectoryContents(root, manifest); err != nil {
		return Manifest{}, err
	}
	seen := make(map[string]struct{}, len(manifest.Assets))
	for index := range manifest.Assets {
		asset := &manifest.Assets[index]
		identity := asset.Target + "\x00" + asset.Kind + "\x00" + asset.FromVersion
		if _, exists := seen[identity]; exists {
			return Manifest{}, fmt.Errorf("duplicate asset route for %s", asset.File)
		}
		seen[identity] = struct{}{}
		if err := verifyAsset(root, *asset, publicKey); err != nil {
			return Manifest{}, err
		}
	}
	sort.Slice(manifest.Assets, func(i, j int) bool {
		left, right := manifest.Assets[i], manifest.Assets[j]
		return left.Target+left.Kind+left.FromVersion < right.Target+right.Kind+right.FromVersion
	})
	return manifest, nil
}

func validateDirectoryContents(root string, manifest Manifest) error {
	expected := map[string]struct{}{ManifestName: {}}
	for _, asset := range manifest.Assets {
		expected[asset.File] = struct{}{}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read release directory: %w", err)
	}
	if len(entries) != len(expected) {
		return errors.New("release directory must contain only the manifest and declared assets")
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok {
			return fmt.Errorf("undeclared release file is forbidden: %s", entry.Name())
		}
		info, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release entry is not a regular file: %s", entry.Name())
		}
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported release schema %d", manifest.SchemaVersion)
	}
	if !identifierPattern.MatchString(manifest.Product) {
		return errors.New("invalid product identifier")
	}
	if manifest.Channel != "stable" && manifest.Channel != "beta" && manifest.Channel != "nightly" {
		return errors.New("invalid release channel")
	}
	targetVersion, err := ParseVersion(manifest.Version)
	if err != nil {
		return err
	}
	if manifest.Channel == "stable" && targetVersion.Pre != "" {
		return errors.New("stable channel cannot publish a prerelease version")
	}
	if manifest.PublishedAt != "" {
		if _, err := time.Parse(time.RFC3339, manifest.PublishedAt); err != nil {
			return errors.New("published_at must be RFC3339")
		}
	}
	if len(manifest.Notes) > 64<<10 {
		return errors.New("release notes are too large")
	}
	if len(manifest.Assets) == 0 || len(manifest.Assets) > MaxReleaseAssets {
		return errors.New("release asset count is invalid")
	}
	fullTargets := make(map[string]bool)
	allTargets := make(map[string]bool)
	for _, asset := range manifest.Assets {
		if !identifierPattern.MatchString(asset.Target) || !filePattern.MatchString(asset.File) {
			return fmt.Errorf("invalid asset identity for %q", asset.File)
		}
		if asset.Size <= 0 || asset.Size > 20<<30 {
			return fmt.Errorf("invalid asset size for %s", asset.File)
		}
		if len(asset.SHA256) != sha256.Size*2 {
			return fmt.Errorf("invalid SHA-256 for %s", asset.File)
		}
		if _, err := hex.DecodeString(asset.SHA256); err != nil {
			return fmt.Errorf("invalid SHA-256 for %s", asset.File)
		}
		if len(asset.Signature) == 0 || len(asset.Signature) > MaxSignatureBytes {
			return fmt.Errorf("invalid signature for %s", asset.File)
		}
		allTargets[asset.Target] = true
		switch asset.Kind {
		case "full":
			if asset.FromVersion != "" {
				return fmt.Errorf("full asset %s cannot have from_version", asset.File)
			}
			fullTargets[asset.Target] = true
		case "delta":
			from, err := ParseVersion(asset.FromVersion)
			if err != nil || !DeltaCompatible(from, targetVersion) {
				return fmt.Errorf("delta %s has incompatible from_version", asset.File)
			}
		default:
			return fmt.Errorf("invalid asset kind for %s", asset.File)
		}
		if len(asset.Mirrors) > 4 {
			return fmt.Errorf("too many mirrors for %s", asset.File)
		}
		for _, mirror := range asset.Mirrors {
			parsed, err := url.Parse(mirror)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
				return fmt.Errorf("invalid HTTPS mirror for %s", asset.File)
			}
		}
	}
	for target := range allTargets {
		if !fullTargets[target] {
			return fmt.Errorf("target %s requires a full fallback asset", target)
		}
	}
	return nil
}

func verifyAsset(root string, asset Asset, publicKey minisign.PublicKey) error {
	path := filepath.Join(root, asset.File)
	if filepath.Dir(path) != root {
		return fmt.Errorf("asset path escapes release root: %s", asset.File)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("asset is absent or unsafe: %s", asset.File)
	}
	if info.Size() != asset.Size {
		return fmt.Errorf("asset size mismatch: %s", asset.File)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	reader := minisign.NewReader(io.TeeReader(file, hash))
	if _, err := io.Copy(io.Discard, reader); err != nil {
		_ = file.Close()
		return fmt.Errorf("read asset %s: %w", asset.File, err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), asset.SHA256) {
		return fmt.Errorf("asset SHA-256 mismatch: %s", asset.File)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(asset.Signature))
	if err != nil || len(signature) > MaxSignatureBytes || !reader.Verify(publicKey, signature) {
		return fmt.Errorf("asset signature mismatch: %s", asset.File)
	}
	return nil
}
