package verify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// HashBody streams the body file through SHA256, also returning the bytes
// and size. For v0.1.0 we buffer the whole body (JSON responses are small);
// when SSE lands in v0.1.1 we'll split hashing from buffering.
func HashBody(path string) (bodyBytes []byte, hexHash string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", 0, fmt.Errorf("open body: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	var buf bytes.Buffer
	n, err := io.Copy(io.MultiWriter(h, &buf), f)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read body: %w", err)
	}
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil)), n, nil
}

// HashBufferedBody computes SHA256 over an already-buffered body (combined-file path).
func HashBufferedBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ParseHashHeader splits a header value like "sha256:abc..." into algorithm
// and hex. We only support sha256 in v0.1.0; an unknown algo prefix returns
// algo == "" so callers can surface the exact wording.
func ParseHashHeader(value string) (algo, hexDigest string, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(parts[0])),
		strings.ToLower(strings.TrimSpace(parts[1])), true
}
