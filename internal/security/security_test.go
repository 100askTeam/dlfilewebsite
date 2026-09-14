package security

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestPasswordHashAndLegacyUpgrade(t *testing.T) {
	password := "a strong release password"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if valid, upgrade := VerifyPassword(hash, password); !valid || upgrade {
		t.Fatalf("bcrypt verification failed: valid=%v upgrade=%v", valid, upgrade)
	}
	if valid, _ := VerifyPassword(hash, "wrong password"); valid {
		t.Fatal("wrong password was accepted")
	}
	digest := sha256.Sum256([]byte(password))
	legacy, err := LegacyPasswordHash(hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if valid, upgrade := VerifyPassword(legacy, password); !valid || !upgrade {
		t.Fatalf("legacy verification failed: valid=%v upgrade=%v", valid, upgrade)
	}
	if !IsKnownDefaultLegacyHash(hex.EncodeToString(sha256Digest("admin123"))) {
		t.Fatal("known public default was not detected")
	}
}

func TestRandomTokensAreHashedAndDistinct(t *testing.T) {
	one, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	two, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if one == two || TokenMatches(TokenHash(one), two) || !TokenMatches(TokenHash(one), one) {
		t.Fatal("token identity contract failed")
	}
}

func TestLoginLimiterBlocksAndRecovers(t *testing.T) {
	limiter := NewLoginLimiter(3, time.Minute, 2*time.Minute)
	now := time.Unix(100, 0)
	for i := 0; i < 3; i++ {
		if allowed, _ := limiter.Allow("127.0.0.1", now); !allowed {
			t.Fatalf("attempt %d blocked too early", i)
		}
		limiter.Failure("127.0.0.1", now)
	}
	if allowed, wait := limiter.Allow("127.0.0.1", now); allowed || wait != 2*time.Minute {
		t.Fatalf("expected block, got allowed=%v wait=%v", allowed, wait)
	}
	if allowed, _ := limiter.Allow("127.0.0.1", now.Add(2*time.Minute)); !allowed {
		t.Fatal("limiter did not recover")
	}
}

func sha256Digest(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}
