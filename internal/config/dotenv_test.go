package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .env files edited on Windows are CRLF by default. godotenv's own parser
// treats the \r as part of the value of an unquoted assignment, which silently
// merges the following lines into the first variable, so loadDotEnv normalises
// line endings first. These tests pin that.
func TestLoadDotEnvParsesEveryLineEnding(t *testing.T) {
	for name, newline := range map[string]string{
		"lf":   "\n",
		"crlf": "\r\n",
		"cr":   "\r",
	} {
		t.Run(name, func(t *testing.T) {
			upper := strings.ToUpper(name)
			plain := "YUNA_TEST_" + upper + "_PLAIN"
			equals := "YUNA_TEST_" + upper + "_EQUALS"
			for _, key := range []string{plain, equals} {
				os.Unsetenv(key)
				t.Cleanup(func() { os.Unsetenv(key) })
			}

			body := "# a comment" + newline +
				newline +
				plain + "=value with spaces" + newline +
				equals + "=has=equals" + newline
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			if err := loadDotEnv(path); err != nil {
				t.Fatalf("loadDotEnv: %v", err)
			}
			if got := os.Getenv(plain); got != "value with spaces" {
				t.Fatalf("%s = %q, want %q", plain, got, "value with spaces")
			}
			if got := os.Getenv(equals); got != "has=equals" {
				t.Fatalf("%s = %q, want %q", equals, got, "has=equals")
			}
		})
	}
}

func TestLoadDotEnvKeepsExistingEnvironment(t *testing.T) {
	const key = "YUNA_TEST_EXISTING"
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(key+"=from-file\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv(key, "from-environment")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	if got := os.Getenv(key); got != "from-environment" {
		t.Fatalf("%s = %q, want the pre-existing value", key, got)
	}
}

func TestLoadDotEnvReportsMissingFile(t *testing.T) {
	err := loadDotEnv(filepath.Join(t.TempDir(), "absent"))
	if !os.IsNotExist(err) {
		t.Fatalf("loadDotEnv = %v, want a not-exist error so callers can ignore it", err)
	}
}
