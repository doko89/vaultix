package agent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Chunked envelope encryption (agent-side only; the manager never sees keys).
//
// Layout:
//
//	magic      6 bytes  "VXENC1"
//	headerLen  u32 BE   length of the JSON header
//	header     JSON     {"key_ref":..., "chunk_size":..., "algo":"aes-256-gcm",
//	                   "nonce_prefix_b64":...}
//	chunks...  each: u32 BE ciphertext length || ciphertext (AES-256-GCM,
//	           nonce = 4-byte random prefix || 8-byte LE counter, tag last 16B)
//
// Chunk plaintext is 1MiB except the last chunk. Decryption verifies the GCM
// tag per chunk, so tampering or a wrong key fails closed.
const (
	EncryptMagic     = "VXENC1"
	EncryptAlgo      = "aes-256-gcm"
	EncryptChunkSize = 1 << 20
	noncePrefixLen   = 4
	maxChunkLen      = 8 << 20
	maxHeaderLen     = 64 << 10
)

type envelopeHeader struct {
	KeyRef         string `json:"key_ref"`
	ChunkSize      int    `json:"chunk_size"`
	Algo           string `json:"algo"`
	NoncePrefixB64 string `json:"nonce_prefix_b64"`
}

// DecodeKey decodes a base64 data key and enforces 32 bytes (AES-256).
func DecodeKey(keyB64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("agent: decode data key: invalid base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("agent: decode data key: want 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

// EncryptStream reads plaintext from r, writes the VXENC1 envelope to w, and
// returns plaintext and envelope byte counts. keyB64 is the base64 data key;
// only keyRef (never key material) lands in the envelope header.
func EncryptStream(w io.Writer, r io.Reader, keyB64, keyRef string) (plainBytes, cipherBytes int64, err error) {
	key, err := DecodeKey(keyB64)
	if err != nil {
		return 0, 0, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return 0, 0, fmt.Errorf("agent: init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, 0, fmt.Errorf("agent: init gcm: %w", err)
	}
	var prefix [noncePrefixLen]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return 0, 0, fmt.Errorf("agent: random nonce prefix: %w", err)
	}
	hdr, err := json.Marshal(envelopeHeader{
		KeyRef:         keyRef,
		ChunkSize:      EncryptChunkSize,
		Algo:           EncryptAlgo,
		NoncePrefixB64: base64.StdEncoding.EncodeToString(prefix[:]),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("agent: encode envelope header: %w", err)
	}
	var written int64
	write := func(p []byte) error {
		n, err := w.Write(p)
		written += int64(n)
		return err
	}
	if err := write([]byte(EncryptMagic)); err != nil {
		return 0, written, fmt.Errorf("agent: write envelope magic: %w", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(hdr)))
	if err := write(lenBuf[:]); err != nil {
		return 0, written, fmt.Errorf("agent: write envelope header length: %w", err)
	}
	if err := write(hdr); err != nil {
		return 0, written, fmt.Errorf("agent: write envelope header: %w", err)
	}

	buf := make([]byte, EncryptChunkSize)
	var counter uint64
	var plain int64
	for {
		n, rerr := io.ReadFull(r, buf)
		if n > 0 {
			nonce := makeNonce(prefix, counter)
			ct := gcm.Seal(nil, nonce, buf[:n], nil)
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ct)))
			if err := write(lenBuf[:]); err != nil {
				return plain, written, fmt.Errorf("agent: write chunk length: %w", err)
			}
			if err := write(ct); err != nil {
				return plain, written, fmt.Errorf("agent: write chunk: %w", err)
			}
			plain += int64(n)
			counter++
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return plain, written, fmt.Errorf("agent: read plaintext: %w", rerr)
		}
	}
	return plain, written, nil
}

// DecryptStream reads a VXENC1 envelope from r and writes plaintext to w.
// A wrong key or any tampering fails with an error and no further output.
func DecryptStream(w io.Writer, r io.Reader, keyB64 string) (plainBytes int64, err error) {
	key, err := DecodeKey(keyB64)
	if err != nil {
		return 0, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return 0, fmt.Errorf("agent: init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, fmt.Errorf("agent: init gcm: %w", err)
	}
	magic := make([]byte, len(EncryptMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return 0, fmt.Errorf("agent: read envelope magic: %w", err)
	}
	if string(magic) != EncryptMagic {
		return 0, fmt.Errorf("agent: bad envelope magic %q (want %q)", magic, EncryptMagic)
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, fmt.Errorf("agent: read envelope header length: %w", err)
	}
	hdrLen := binary.BigEndian.Uint32(lenBuf[:])
	if hdrLen == 0 || hdrLen > maxHeaderLen {
		return 0, fmt.Errorf("agent: bad envelope header length %d", hdrLen)
	}
	hdrRaw := make([]byte, hdrLen)
	if _, err := io.ReadFull(r, hdrRaw); err != nil {
		return 0, fmt.Errorf("agent: read envelope header: %w", err)
	}
	var hdr envelopeHeader
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil {
		return 0, fmt.Errorf("agent: decode envelope header: %w", err)
	}
	if hdr.Algo != EncryptAlgo {
		return 0, fmt.Errorf("agent: unsupported envelope algo %q", hdr.Algo)
	}
	prefixRaw, err := base64.StdEncoding.DecodeString(hdr.NoncePrefixB64)
	if err != nil || len(prefixRaw) != noncePrefixLen {
		return 0, fmt.Errorf("agent: bad envelope nonce prefix")
	}
	var prefix [noncePrefixLen]byte
	copy(prefix[:], prefixRaw)

	var counter uint64
	var plain int64
	for {
		_, err := io.ReadFull(r, lenBuf[:])
		if err == io.EOF {
			return plain, nil
		}
		if err == io.ErrUnexpectedEOF {
			return plain, fmt.Errorf("agent: truncated envelope at chunk %d", counter)
		}
		if err != nil {
			return plain, fmt.Errorf("agent: read chunk length: %w", err)
		}
		clen := binary.BigEndian.Uint32(lenBuf[:])
		if clen == 0 || clen > uint32(maxChunkLen+gcm.Overhead()) {
			return plain, fmt.Errorf("agent: bad chunk %d length %d", counter, clen)
		}
		ct := make([]byte, clen)
		if _, err := io.ReadFull(r, ct); err != nil {
			return plain, fmt.Errorf("agent: read chunk %d: %w", counter, err)
		}
		pt, err := gcm.Open(nil, makeNonce(prefix, counter), ct, nil)
		if err != nil {
			return plain, fmt.Errorf("agent: chunk %d authentication failed (wrong key or tampered data)", counter)
		}
		if _, err := w.Write(pt); err != nil {
			return plain, fmt.Errorf("agent: write plaintext: %w", err)
		}
		plain += int64(len(pt))
		counter++
	}
}

// EncryptWriter is the streaming counterpart to EncryptStream: plaintext
// written to it lands on the underlying writer as a VXENC1 envelope.
// Close flushes the final partial chunk and must always be called.
type EncryptWriter struct {
	dst     io.Writer
	gcm     cipher.AEAD
	prefix  [noncePrefixLen]byte
	counter uint64
	buf     []byte
	plain   int64
	closed  bool
}

// NewEncryptWriter writes the envelope header to dst immediately and returns
// a writer for plaintext. Only keyRef lands in the header.
func NewEncryptWriter(dst io.Writer, keyB64, keyRef string) (*EncryptWriter, error) {
	key, err := DecodeKey(keyB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("agent: init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("agent: init gcm: %w", err)
	}
	var prefix [noncePrefixLen]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return nil, fmt.Errorf("agent: random nonce prefix: %w", err)
	}
	hdr, err := json.Marshal(envelopeHeader{
		KeyRef:         keyRef,
		ChunkSize:      EncryptChunkSize,
		Algo:           EncryptAlgo,
		NoncePrefixB64: base64.StdEncoding.EncodeToString(prefix[:]),
	})
	if err != nil {
		return nil, fmt.Errorf("agent: encode envelope header: %w", err)
	}
	ew := &EncryptWriter{dst: dst, gcm: gcm, prefix: prefix, buf: make([]byte, 0, EncryptChunkSize)}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(hdr)))
	for _, p := range [][]byte{[]byte(EncryptMagic), lenBuf[:], hdr} {
		if _, err := dst.Write(p); err != nil {
			return nil, fmt.Errorf("agent: write envelope header: %w", err)
		}
	}
	return ew, nil
}

// Write buffers plaintext and emits full 1MiB chunks.
func (w *EncryptWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("agent: write to closed encrypt writer")
	}
	total := len(p)
	w.plain += int64(total)
	for len(p) > 0 {
		space := EncryptChunkSize - len(w.buf)
		n := len(p)
		if n > space {
			n = space
		}
		w.buf = append(w.buf, p[:n]...)
		p = p[n:]
		if len(w.buf) == EncryptChunkSize {
			if err := w.emit(w.buf); err != nil {
				return total - len(p), err
			}
			w.buf = w.buf[:0]
		}
	}
	return total, nil
}

// Close emits the final partial chunk (if any).
func (w *EncryptWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.buf) > 0 {
		if err := w.emit(w.buf); err != nil {
			return err
		}
		w.buf = nil
	}
	return nil
}

// PlainBytes returns plaintext bytes accepted so far.
func (w *EncryptWriter) PlainBytes() int64 { return w.plain }

func (w *EncryptWriter) emit(plain []byte) error {
	ct := w.gcm.Seal(nil, makeNonce(w.prefix, w.counter), plain, nil)
	w.counter++
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ct)))
	for _, p := range [][]byte{lenBuf[:], ct} {
		if _, err := w.dst.Write(p); err != nil {
			return fmt.Errorf("agent: write envelope chunk: %w", err)
		}
	}
	return nil
}

func makeNonce(prefix [noncePrefixLen]byte, counter uint64) []byte {
	nonce := make([]byte, 0, noncePrefixLen+8)
	nonce = append(nonce, prefix[:]...)
	var ctr [8]byte
	binary.LittleEndian.PutUint64(ctr[:], counter)
	return append(nonce, ctr[:]...)
}
