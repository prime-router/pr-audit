// Package verify owns the L1 self-consistency pipeline:
// parse the saved response, detect the upstream, hash the body, and
// compare against the declared x-upstream-sha256.
package verify

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"os"
	"strconv"
	"strings"

	"github.com/primerouter/pr-audit/internal/model"
)

// ParseSplit reads a headers file (from `curl -D`) and a body file (from `curl -o`).
// The body file is the hash target — its bytes must be exactly what the
// SDK's response.content would expose (see specs §2.4).
func ParseSplit(headersPath, bodyPath string) (*model.Response, error) {
	hf, err := os.Open(headersPath)
	if err != nil {
		return nil, fmt.Errorf("open headers: %w", err)
	}
	defer hf.Close()

	br := bufio.NewReader(hf)
	var (
		status  int
		headers http.Header
	)
	for {
		status, headers, err = readHeaderBlock(br)
		if err != nil {
			return nil, fmt.Errorf("parse headers %s: %w", headersPath, err)
		}
		if status >= 200 {
			break
		}
	}

	// Verify body path exists & is readable; we stream it later.
	if st, err := os.Stat(bodyPath); err != nil {
		return nil, fmt.Errorf("stat body: %w", err)
	} else if st.IsDir() {
		return nil, fmt.Errorf("body path %s is a directory", bodyPath)
	}

	return &model.Response{
		StatusCode: status,
		Headers:    headers,
		BodyPath:   bodyPath,
		Source:     fmt.Sprintf("split:%s+%s", headersPath, bodyPath),
	}, nil
}

// ParseCombined reads a `curl -i` style file: status line + headers + blank
// line + body. Body bytes are returned inline; callers hash from BodyInline
// rather than re-opening a file.
func ParseCombined(path string) (*model.Response, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open response: %w", err)
	}

	status, headers, body, err := splitCombined(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &model.Response{
		StatusCode: status,
		Headers:    headers,
		BodyInline: body,
		Source:     "combined:" + path,
	}, nil
}

// splitCombined finds the header/body boundary in a curl -i dump.
// It skips any leading 1xx (informational) responses and returns the
// last >=200 status line's headers plus everything after its blank line.
func splitCombined(data []byte) (int, http.Header, []byte, error) {
	rest := data
	for {
		sep, sepLen := findHeaderBodySeparator(rest)
		if sep < 0 {
			return 0, nil, nil, errors.New("could not locate blank line separating headers and body")
		}
		headerBytes := rest[:sep]
		body := rest[sep+sepLen:]

		status, headers, err := readHeaderBlock(bufio.NewReader(bytes.NewReader(headerBytes)))
		if err != nil {
			return 0, nil, nil, err
		}
		if status >= 200 {
			return status, headers, body, nil
		}
		// informational; keep scanning the remainder for the real response
		rest = body
	}
}

// findHeaderBodySeparator returns the index of the first CRLFCRLF or LFLF,
// plus its length. Returns -1 if none found.
func findHeaderBodySeparator(data []byte) (int, int) {
	i := bytes.Index(data, []byte("\r\n\r\n"))
	j := bytes.Index(data, []byte("\n\n"))
	switch {
	case i < 0 && j < 0:
		return -1, 0
	case i < 0:
		return j, 2
	case j < 0:
		return i, 4
	case i <= j:
		return i, 4
	default:
		return j, 2
	}
}

// readHeaderBlock parses one HTTP status line + its MIME header block.
// CRLF/LF tolerant. Returns on the first status line found; callers that
// want to skip 1xx responses should loop while status < 200.
func readHeaderBlock(br *bufio.Reader) (int, http.Header, error) {
	var statusLine string
	for {
		line, err := readLine(br)
		if err != nil {
			return 0, nil, err
		}
		if line == "" {
			// tolerate blank lines before the status line (e.g. trailing blank
			// from a previous informational block)
			continue
		}
		statusLine = line
		break
	}
	status, err := parseStatusLine(statusLine)
	if err != nil {
		return 0, nil, err
	}
	tp := textproto.NewReader(br)
	mime, err := tp.ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, fmt.Errorf("read headers: %w", err)
	}
	return status, http.Header(mime), nil
}

func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func parseStatusLine(line string) (int, error) {
	if !strings.HasPrefix(line, "HTTP/") {
		return 0, fmt.Errorf("not an HTTP status line: %q", truncate(line, 60))
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("malformed status line: %q", truncate(line, 60))
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("status code %q not an int", parts[1])
	}
	return code, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
