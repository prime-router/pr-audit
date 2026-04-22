package verify

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSplit_OK(t *testing.T) {
	resp, err := ParseSplit(
		filepath.Join("..", "..", "testdata", "mocks", "openai-ok.headers.txt"),
		filepath.Join("..", "..", "testdata", "mocks", "openai-ok.body.json"),
	)
	if err != nil {
		t.Fatalf("ParseSplit: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Headers.Get("X-Upstream-Vendor"); got != "openai" {
		t.Errorf("x-upstream-vendor = %q, want openai", got)
	}
	if !strings.HasPrefix(resp.Headers.Get("X-Upstream-Sha256"), "sha256:") {
		t.Errorf("x-upstream-sha256 missing sha256: prefix, got %q",
			resp.Headers.Get("X-Upstream-Sha256"))
	}
	if resp.BodyPath == "" {
		t.Error("BodyPath empty")
	}
}

func TestParseSplit_MissingBody(t *testing.T) {
	_, err := ParseSplit(
		filepath.Join("..", "..", "testdata", "mocks", "openai-ok.headers.txt"),
		filepath.Join("..", "..", "testdata", "mocks", "does-not-exist.bin"),
	)
	if err == nil {
		t.Fatal("expected error for missing body, got nil")
	}
}

func TestSplitCombined_SkipsInformational(t *testing.T) {
	body, status, headers, err := parseCombinedFile(t, "combined-with-continue.txt")
	if err != nil {
		t.Fatalf("splitCombined: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200 (1xx should be skipped)", status)
	}
	if headers.Get("X-Upstream-Vendor") != "zhipu" {
		t.Errorf("vendor header = %q, want zhipu", headers.Get("X-Upstream-Vendor"))
	}
	if !strings.Contains(string(body), `"glm-4-flash"`) {
		t.Errorf("body does not contain expected model field; got %q", body)
	}
}

func TestSplitCombined_NoSeparator(t *testing.T) {
	_, _, _, err := splitCombined([]byte("HTTP/2 200\r\ncontent-type: text/plain\r\nno blank line here"))
	if err == nil {
		t.Fatal("expected error when separator missing")
	}
}

func TestSplitCombined_NotHTTP(t *testing.T) {
	_, _, _, err := splitCombined([]byte("this is not an http response\r\n\r\nbody"))
	if err == nil {
		t.Fatal("expected error for non-HTTP input")
	}
}

func TestParseStatusLine(t *testing.T) {
	cases := []struct {
		in      string
		code    int
		wantErr bool
	}{
		{"HTTP/2 200", 200, false},
		{"HTTP/1.1 404 Not Found", 404, false},
		{"HTTP/1.1 100 Continue", 100, false},
		{"notvalid", 0, true},
		{"HTTP/1.1", 0, true},
		{"HTTP/1.1 XYZ", 0, true},
	}
	for _, c := range cases {
		got, err := parseStatusLine(c.in)
		if c.wantErr && err == nil {
			t.Errorf("parseStatusLine(%q): want err, got nil", c.in)
			continue
		}
		if !c.wantErr && err != nil {
			t.Errorf("parseStatusLine(%q): unexpected err %v", c.in, err)
			continue
		}
		if got != c.code {
			t.Errorf("parseStatusLine(%q) = %d, want %d", c.in, got, c.code)
		}
	}
}

func parseCombinedFile(t *testing.T, name string) ([]byte, int, http.Header, error) {
	t.Helper()
	resp, err := ParseCombined(filepath.Join("..", "..", "testdata", "mocks", name))
	if err != nil {
		return nil, 0, nil, err
	}
	return resp.BodyInline, resp.StatusCode, resp.Headers, nil
}
