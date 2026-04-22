package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestHashBufferedBody_KnownVector(t *testing.T) {
	// sha256("") — the RFC 6234 test vector
	if got := HashBufferedBody(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("empty hash mismatch: %s", got)
	}
	// sha256("hi") — the classic
	if got := HashBufferedBody([]byte("hi")); got != "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4" {
		t.Errorf("hi hash mismatch: %s", got)
	}
}

func TestHashBody_MatchesDirectSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.bin")
	data := []byte(`{"hello":"world"}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	gotBytes, gotHex, size, err := HashBody(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	wantHex := hex.EncodeToString(want[:])
	if gotHex != wantHex {
		t.Errorf("hex = %s, want %s", gotHex, wantHex)
	}
	if string(gotBytes) != string(data) {
		t.Errorf("bytes roundtrip mismatch")
	}
	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}
}

func TestHashBody_Missing(t *testing.T) {
	if _, _, _, err := HashBody("/definitely/does/not/exist.bin"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseHashHeader(t *testing.T) {
	cases := []struct {
		in         string
		algo, hexd string
		ok         bool
	}{
		{"sha256:ABC", "sha256", "abc", true},
		{" sha256:abc ", "sha256", "abc", true},
		{"SHA256:ABC", "sha256", "abc", true},
		{"sha3-256:abc", "sha3-256", "abc", true}, // parsed but unsupported (caller decides)
		{"no-colon", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		algo, hexd, ok := ParseHashHeader(c.in)
		if ok != c.ok {
			t.Errorf("ParseHashHeader(%q) ok=%v, want %v", c.in, ok, c.ok)
		}
		if algo != c.algo || hexd != c.hexd {
			t.Errorf("ParseHashHeader(%q) = (%q, %q), want (%q, %q)",
				c.in, algo, hexd, c.algo, c.hexd)
		}
	}
}
