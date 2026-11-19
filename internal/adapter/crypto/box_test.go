package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	b, err := NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	b := testBox(t)
	secret := "super-secret-client-secret-value"
	env, err := b.Encrypt([]byte(secret))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(env, secret) {
		t.Fatalf("envelope leaks plaintext: %q", env)
	}
	got, err := b.Decrypt(env)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != secret {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestEncryptIsRandomized(t *testing.T) {
	b := testBox(t)
	a, _ := b.Encrypt([]byte("same"))
	c, _ := b.Encrypt([]byte("same"))
	if a == c {
		t.Fatal("two encryptions of the same plaintext must differ (random nonce)")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	b := testBox(t)
	env, err := b.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decrypt(env); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	b := testBox(t)
	env, err := b.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := env[:len(env)-2] + "AA"
	if _, err := b.Decrypt(tampered); err == nil {
		t.Fatal("decrypt of tampered envelope must fail")
	}
	for _, bad := range []string{"", "no-separator", "v9:eA==", "v1:!!!not-base64!!!", "v1:eA=="} {
		if _, err := b.Decrypt(bad); err == nil {
			t.Fatalf("decrypt(%q) must fail", bad)
		}
	}
}

func TestNewBoxRejectsBadKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 24, 31, 33, 64} {
		if _, err := NewBox(make([]byte, n)); err == nil {
			t.Fatalf("NewBox with %d bytes must fail", n)
		}
	}
}

func TestNewBoxFromEnvFormats(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(0xA0 + i)
	}
	formats := map[string]string{
		"raw":    string(key),
		"std64":  base64.StdEncoding.EncodeToString(key),
		"url64":  base64.URLEncoding.EncodeToString(key),
		"raw64":  base64.RawStdEncoding.EncodeToString(key),
		"rawurl": base64.RawURLEncoding.EncodeToString(key),
	}
	for name, val := range formats {
		t.Run(name, func(t *testing.T) {
			t.Setenv("VAULTIX_CRYPTO_KEY_TEST", val)
			b, err := NewBoxFromEnv("VAULTIX_CRYPTO_KEY_TEST")
			if err != nil {
				t.Fatalf("NewBoxFromEnv(%s): %v", name, err)
			}
			env, err := b.Encrypt([]byte("x"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Decrypt(env); err != nil {
				t.Fatalf("round trip (%s): %v", name, err)
			}
		})
	}
}

func TestNewBoxFromEnvHex(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv("VAULTIX_CRYPTO_KEY_HEX", hex.EncodeToString(key))
	b, err := NewBoxFromEnv("VAULTIX_CRYPTO_KEY_HEX")
	if err != nil {
		t.Fatalf("hex key: %v", err)
	}
	env, err := b.Encrypt([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Decrypt(env); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAULTIX_CRYPTO_KEY_HEX", "0x"+hex.EncodeToString(key))
	if _, err := NewBoxFromEnv("VAULTIX_CRYPTO_KEY_HEX"); err != nil {
		t.Fatalf("0x-prefixed hex key: %v", err)
	}
}

func TestNilBoxFailsClosed(t *testing.T) {
	var b *Box
	if _, err := b.Encrypt([]byte("x")); err == nil {
		t.Fatal("nil Encrypt must fail")
	}
	if _, err := b.Decrypt("v1:eA=="); err == nil {
		t.Fatal("nil Decrypt must fail")
	}
}

func TestNewBoxFromEnvMissing(t *testing.T) {
	os.Unsetenv("VAULTIX_CRYPTO_KEY_TEST_MISSING")
	if _, err := NewBoxFromEnv("VAULTIX_CRYPTO_KEY_TEST_MISSING"); err == nil {
		t.Fatal("missing env key must fail")
	}
	t.Setenv("VAULTIX_CRYPTO_KEY_TEST_MISSING", "too-short")
	if _, err := NewBoxFromEnv("VAULTIX_CRYPTO_KEY_TEST_MISSING"); err == nil {
		t.Fatal("short env key must fail")
	}
}

func TestErrorsLeakNothing(t *testing.T) {
	b := testBox(t)
	secret := "TOP-SECRET-PLAINTEXT-12345"
	env, err := b.Encrypt([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewBox(make([]byte, 32))
	errs := []error{}
	if _, err := other.Decrypt(env); err != nil {
		errs = append(errs, err)
	}
	if _, err := b.Decrypt("v1:corrupted"); err != nil {
		errs = append(errs, err)
	}
	if _, err := NewBox([]byte("short")); err != nil {
		errs = append(errs, err)
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks secret: %v", err)
		}
	}
}

func TestGenerateKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 || len(k2) != 32 {
		t.Fatalf("keys must be 32 bytes, got %d/%d", len(k1), len(k2))
	}
	if string(k1) == string(k2) {
		t.Fatal("generated keys must differ")
	}
	if _, err := NewBox(k1); err != nil {
		t.Fatalf("generated key unusable: %v", err)
	}
}
