package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheInternetUse7/yuna/internal/applog"
)

// Bifrost logs its "no primary error" line after every successful request,
// which restates what the reply attribution already records. The adapter drops
// it so the debug log keeps room for attempts, failures and fallbacks.
func TestBifrostLoggerDropsRedundantSuccessLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yuna.log")
	log, err := applog.New(path, true)
	if err != nil {
		t.Fatalf("applog.New: %v", err)
	}
	defer func() { _ = log.Close() }()

	logger := newBifrostLogger(log)
	logger.Debug(noPrimaryErrorMessage)
	logger.Debug("primary provider vercel with model google/gemini-2.5-flash and 1 fallbacks")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(raw)
	if strings.Contains(got, noPrimaryErrorMessage) {
		t.Fatalf("log %q should not contain %q", got, noPrimaryErrorMessage)
	}
	if !strings.Contains(got, "primary provider vercel") {
		t.Fatalf("log %q is missing the kept debug line", got)
	}
}
