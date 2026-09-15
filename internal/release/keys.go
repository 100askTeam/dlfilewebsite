package release

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aead.dev/minisign"
)

const MaxPublicKeyBytes = 16 << 10

var ErrPublicKeyNotConfigured = errors.New("release public key is not configured")

// PublicKeyring binds one immutable product slug to exactly one updater key.
// There is deliberately no wildcard or trial-verification fallback: a
// manifest can be trusted only inside the product signing domain it names.
type PublicKeyring struct {
	keys map[string]minisign.PublicKey
}

func NewPublicKeyring(values map[string]string) (*PublicKeyring, error) {
	if len(values) == 0 {
		return nil, ErrPublicKeyNotConfigured
	}
	keyring := &PublicKeyring{keys: make(map[string]minisign.PublicKey, len(values))}
	for product, value := range values {
		if !identifierPattern.MatchString(product) {
			return nil, fmt.Errorf("invalid release key product %q", product)
		}
		key, err := DecodePublicKey(value)
		if err != nil {
			return nil, fmt.Errorf("decode release key for %s: %w", product, err)
		}
		keyring.keys[product] = key
	}
	return keyring, nil
}

func (k *PublicKeyring) KeyFor(product string) (minisign.PublicKey, error) {
	if k == nil {
		return minisign.PublicKey{}, ErrPublicKeyNotConfigured
	}
	key, ok := k.keys[product]
	if !ok {
		return minisign.PublicKey{}, fmt.Errorf("release product %q has no configured public key", product)
	}
	return key, nil
}

func LoadPublicKeyringDirectory(directory string) (*PublicKeyring, error) {
	root, err := filepath.Abs(strings.TrimSpace(directory))
	if err != nil {
		return nil, fmt.Errorf("resolve release key directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("release key directory is absent, unsafe, or not a directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read release key directory: %w", err)
	}
	values := make(map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() || filepath.Ext(name) != ".pub" {
			return nil, fmt.Errorf("unexpected release key entry %q", name)
		}
		product := strings.TrimSuffix(name, ".pub")
		if !identifierPattern.MatchString(product) {
			return nil, fmt.Errorf("invalid release key filename %q", name)
		}
		path := filepath.Join(root, name)
		fileInfo, err := os.Lstat(path)
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 || fileInfo.Size() > MaxPublicKeyBytes {
			return nil, fmt.Errorf("release key %q is absent, unsafe, or too large", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read release key %q: %w", name, err)
		}
		values[product] = string(data)
	}
	return NewPublicKeyring(values)
}

// LoadConfiguredPublicKeyring prefers the product-key directory. The legacy
// single-key configuration remains scoped to LYNX only, so it cannot silently
// authorize a second product.
func LoadConfiguredPublicKeyring() (*PublicKeyring, error) {
	if directory := strings.TrimSpace(os.Getenv("DL_RELEASE_PUBLIC_KEYS_DIR")); directory != "" {
		return LoadPublicKeyringDirectory(directory)
	}
	keyText := strings.TrimSpace(os.Getenv("DL_RELEASE_PUBLIC_KEY"))
	if keyFile := strings.TrimSpace(os.Getenv("DL_RELEASE_PUBLIC_KEY_FILE")); keyFile != "" {
		data, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("read DL_RELEASE_PUBLIC_KEY_FILE: %w", err)
		}
		keyText = string(data)
	}
	if strings.TrimSpace(keyText) == "" {
		return nil, ErrPublicKeyNotConfigured
	}
	return NewPublicKeyring(map[string]string{"lynx": keyText})
}
