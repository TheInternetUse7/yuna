package config

import "testing"

// Bifrost appends the API path to the configured base URL (the OpenAI driver
// requests BaseURL+"/v1/chat/completions"). Both spellings
// are accepted in YAML, and both must land on the bare host.
func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://ai-gateway.vercel.sh/v1":    "https://ai-gateway.vercel.sh",
		"https://ai-gateway.vercel.sh/v1/":   "https://ai-gateway.vercel.sh",
		"https://ai-gateway.vercel.sh":       "https://ai-gateway.vercel.sh",
		"https://ai-gateway.vercel.sh/":      "https://ai-gateway.vercel.sh",
		"  https://agentrouter.org/v1  ":     "https://agentrouter.org",
		"http://127.0.0.1:8000/v1":           "http://127.0.0.1:8000",
		"http://host.docker.internal:11434":  "http://host.docker.internal:11434",
		"":                                   "",
		"https://gateway.example/other/path": "https://gateway.example/other/path",
	}
	for raw, want := range cases {
		if got := normalizeBaseURL(raw); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
