package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/applog"
)

// Model attribution belongs in the log and nowhere else: a Discord reply must
// contain only what the model wrote.
func TestLogAttributionRecordsTheAnsweringModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yuna.log")
	log, err := applog.New(path, false)
	if err != nil {
		t.Fatalf("applog.New: %v", err)
	}
	defer func() { _ = log.Close() }()

	b := &Bot{log: log}

	b.logAttribution("reply in channel 42", &ai.Response{
		Provider:   "groq",
		Model:      "openai/gpt-oss-120b",
		IsFallback: true,
	})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, want := range []string{
		"reply in channel 42",
		"provider=groq",
		"model=openai/gpt-oss-120b",
		"fallback=true",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("log line %q is missing %q", raw, want)
		}
	}
}
