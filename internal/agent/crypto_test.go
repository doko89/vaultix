package agent

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKeyB64(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	_, err := rand.Read(raw[:])
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw[:])
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKeyB64(t)
	// Multi-chunk payload: 2.5 MiB forces 3 chunks through EncryptStream.
	plain := bytes.Repeat([]byte("vaultix-backup-payload-0123456789"), 80000)
	var env bytes.Buffer
	pn, cn, err := EncryptStream(&env, bytes.NewReader(plain), key, "key-ref-1")
	require.NoError(t, err)
	assert.Equal(t, int64(len(plain)), pn)
	assert.Greater(t, cn, pn, "envelope adds header + tags + framing")

	assert.True(t, strings.HasPrefix(env.String(), EncryptMagic))
	assert.Contains(t, env.String(), `"key_ref":"key-ref-1"`)
	assert.Contains(t, env.String(), `"algo":"aes-256-gcm"`)
	assert.NotContains(t, env.String(), key, "envelope must not embed key material")
	assert.NotContains(t, env.String(), "vaultix-backup-payload", "payload must be ciphertext")

	var out bytes.Buffer
	n, err := DecryptStream(&out, &env, key)
	require.NoError(t, err)
	assert.Equal(t, int64(len(plain)), n)
	assert.Equal(t, plain, out.Bytes())
}

func TestEncryptWriterRoundTrip(t *testing.T) {
	key := testKeyB64(t)
	plain := bytes.Repeat([]byte{0xA5, 0x5A}, 3<<20) // 6 MiB, odd chunk boundary writes
	var env bytes.Buffer
	ew, err := NewEncryptWriter(&env, key, "k")
	require.NoError(t, err)
	// Ragged writes: 100KB, 2MB, rest — chunking must stay aligned.
	chunks := [][]byte{plain[:100<<10], plain[100<<10 : 100<<10+2<<20], plain[100<<10+2<<20:]}
	for _, c := range chunks {
		_, err := ew.Write(c)
		require.NoError(t, err)
	}
	require.NoError(t, ew.Close())
	assert.Equal(t, int64(len(plain)), ew.PlainBytes())

	var out bytes.Buffer
	_, err = DecryptStream(&out, &env, key)
	require.NoError(t, err)
	assert.Equal(t, plain, out.Bytes())
}

func TestEncryptEmptyInput(t *testing.T) {
	key := testKeyB64(t)
	var env bytes.Buffer
	_, _, err := EncryptStream(&env, bytes.NewReader(nil), key, "k")
	require.NoError(t, err)
	var out bytes.Buffer
	n, err := DecryptStream(&out, &env, key)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, out.Bytes())
}

func TestDecryptDetectsTamper(t *testing.T) {
	key := testKeyB64(t)
	plain := bytes.Repeat([]byte("tamper-me"), 200000)
	var env bytes.Buffer
	_, _, err := EncryptStream(&env, bytes.NewReader(plain), key, "k")
	require.NoError(t, err)

	tampered := append([]byte(nil), env.Bytes()...)
	// Flip a byte in the last chunk's ciphertext (past header + first chunk).
	tampered[len(tampered)-10] ^= 0xFF
	var out bytes.Buffer
	_, err = DecryptStream(&out, bytes.NewReader(tampered), key)
	require.ErrorContains(t, err, "authentication failed")
}

func TestDecryptWrongKeyFails(t *testing.T) {
	var env bytes.Buffer
	_, _, err := EncryptStream(&env, bytes.NewReader([]byte("secret")), testKeyB64(t), "k")
	require.NoError(t, err)
	var out bytes.Buffer
	_, err = DecryptStream(&out, &env, testKeyB64(t))
	require.ErrorContains(t, err, "authentication failed")
}

func TestDecryptRejectsBadEnvelope(t *testing.T) {
	key := testKeyB64(t)
	for name, input := range map[string][]byte{
		"bad magic":   []byte("BOGUS1\x00\x00\x00\x01{}"),
		"truncated":   []byte("VXEN"),
		"empty":       nil,
		"huge header": append([]byte("VXENC1\xff\xff\xff\xff"), bytes.Repeat([]byte("x"), 100)...),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			_, err := DecryptStream(&out, bytes.NewReader(input), key)
			require.Error(t, err)
		})
	}
}

func TestDecodeKeyEnforcesAES256(t *testing.T) {
	_, err := DecodeKey("!!!not-base64!!!")
	require.Error(t, err)
	_, err = DecodeKey(base64.StdEncoding.EncodeToString([]byte("short")))
	require.ErrorContains(t, err, "want 32 bytes")
	k, err := DecodeKey(testKeyB64(t))
	require.NoError(t, err)
	assert.Len(t, k, 32)
}
