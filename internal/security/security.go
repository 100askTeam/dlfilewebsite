package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const legacyPrefix = "legacy-sha256$"

var ErrWeakPassword = errors.New("password must be at least 12 characters")

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 256 {
		return "", ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(stored, password string) (valid bool, needsUpgrade bool) {
	if len(password) > 256 {
		return false, false
	}
	if strings.HasPrefix(stored, legacyPrefix) {
		expected, err := hex.DecodeString(strings.TrimPrefix(stored, legacyPrefix))
		if err != nil || len(expected) != sha256.Size {
			return false, false
		}
		actual := sha256.Sum256([]byte(password))
		return subtle.ConstantTimeCompare(expected, actual[:]) == 1, true
	}
	return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil, false
}

func LegacyPasswordHash(hexDigest string) (string, error) {
	digest, err := hex.DecodeString(strings.TrimSpace(hexDigest))
	if err != nil || len(digest) != sha256.Size {
		return "", errors.New("invalid legacy password digest")
	}
	return legacyPrefix + hex.EncodeToString(digest), nil
}

func IsKnownDefaultLegacyHash(hexDigest string) bool {
	digest := sha256.Sum256([]byte("admin123"))
	return subtle.ConstantTimeCompare(
		[]byte(strings.ToLower(strings.TrimSpace(hexDigest))),
		[]byte(hex.EncodeToString(digest[:])),
	) == 1
}

func RandomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func TokenHash(raw string) []byte {
	digest := sha256.Sum256([]byte(raw))
	return digest[:]
}

func TokenMatches(expected []byte, raw string) bool {
	actual := TokenHash(raw)
	return len(expected) == len(actual) && subtle.ConstantTimeCompare(expected, actual) == 1
}

type loginAttempt struct {
	windowStarted time.Time
	failures      int
	blockedUntil  time.Time
}

type LoginLimiter struct {
	mu          sync.Mutex
	attempts    map[string]loginAttempt
	maxFailures int
	window      time.Duration
	block       time.Duration
}

func NewLoginLimiter(maxFailures int, window, block time.Duration) *LoginLimiter {
	return &LoginLimiter{
		attempts:    make(map[string]loginAttempt),
		maxFailures: maxFailures,
		window:      window,
		block:       block,
	}
}

func (l *LoginLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[key]
	if !ok {
		return true, 0
	}
	if now.Before(attempt.blockedUntil) {
		return false, attempt.blockedUntil.Sub(now)
	}
	if now.Sub(attempt.windowStarted) >= l.window {
		delete(l.attempts, key)
	}
	return true, 0
}

func (l *LoginLimiter) Failure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	if attempt.windowStarted.IsZero() || now.Sub(attempt.windowStarted) >= l.window {
		attempt = loginAttempt{windowStarted: now}
	}
	attempt.failures++
	if attempt.failures >= l.maxFailures {
		attempt.blockedUntil = now.Add(l.block)
	}
	l.attempts[key] = attempt
}

func (l *LoginLimiter) Success(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}
