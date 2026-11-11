package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveHTTPBin runs every testdata file against a real httpbin and fails on
// any request or assertion error. It is opt-in because it needs network access:
//
//	HTTPCLI_LIVE=1 go test ./internal/cli -run TestLiveHTTPBin
//
// HTTPBIN_BASE_URL overrides the default target (https://httpbin.org).
func TestLiveHTTPBin(t *testing.T) {
	if os.Getenv("HTTPCLI_LIVE") == "" {
		t.Skip("set HTTPCLI_LIVE=1 to run the testdata suite against a live httpbin")
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "*.http"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .http files in testdata")
	}
	for i, file := range files {
		if i > 0 {
			// httpbin.org rate-limits bursts, so one file per second keeps the
			// suite green against the default instance.
			time.Sleep(time.Second)
		}
		t.Run(filepath.Base(file), func(t *testing.T) {
			code, stdout, stderr := invoke(t, "run", "-all", "-output", "none", file)
			if code != 0 {
				t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
		})
	}
}
