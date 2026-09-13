package ai

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/config"
)

func TestParseSummaryJSON(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantSummary string
		wantFacts   int
		wantErr     bool
	}{
		{
			name:        "plain object",
			raw:         `{"summary":"they talked about tea","facts":[{"user":"alice","fact":"likes tea"}]}`,
			wantSummary: "they talked about tea",
			wantFacts:   1,
		},
		{
			name: "fenced with a language tag",
			raw: "```json\n" +
				`{"summary":"s","facts":[]}` + "\n```",
			wantSummary: "s",
		},
		{
			name:        "fenced with no language tag",
			raw:         "```\n" + `{"summary":"s"}` + "\n```",
			wantSummary: "s",
		},
		{
			name:        "prose before the object",
			raw:         "Sure, here you go:\n" + `{"summary":"s","facts":[{"user":"a","fact":"f"}]}`,
			wantSummary: "s",
			wantFacts:   1,
		},
		{
			name:        "missing facts key is tolerated",
			raw:         `{"summary":"s"}`,
			wantSummary: "s",
		},
		{
			name:        "null facts is tolerated",
			raw:         `{"summary":"s","facts":null}`,
			wantSummary: "s",
		},
		{
			name:    "not JSON at all",
			raw:     "I could not summarise that.",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSummaryJSON(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSummaryJSON: %v", err)
			}
			if got.Summary != tc.wantSummary {
				t.Fatalf("summary = %q, want %q", got.Summary, tc.wantSummary)
			}
			if len(got.Facts) != tc.wantFacts {
				t.Fatalf("got %d facts, want %d: %+v", len(got.Facts), tc.wantFacts, got.Facts)
			}
		})
	}
}

func TestParseSummaryJSONDropsIncompleteFacts(t *testing.T) {
	got, err := ParseSummaryJSON(`{"summary":"s","facts":[
		{"user":"alice","fact":"likes tea"},
		{"user":"","fact":"orphan"},
		{"user":"bob","fact":"   "},
		{"user":"carol","fact":"writes Go"}
	]}`)
	if err != nil {
		t.Fatalf("ParseSummaryJSON: %v", err)
	}
	if len(got.Facts) != 2 {
		t.Fatalf("got %d facts, want 2 (blank user and blank fact dropped): %+v", len(got.Facts), got.Facts)
	}
	if got.Facts[0].User != "alice" || got.Facts[1].User != "carol" {
		t.Fatalf("facts = %+v", got.Facts)
	}
}

func TestParseSummaryJSONTakesOutermostObject(t *testing.T) {
	// A nested object must not truncate the parse.
	got, err := ParseSummaryJSON(`{"summary":"s","meta":{"nested":true},"facts":[]}`)
	if err != nil {
		t.Fatalf("ParseSummaryJSON: %v", err)
	}
	if got.Summary != "s" {
		t.Fatalf("summary = %q, want s", got.Summary)
	}
}

// testProviders builds a chain shaped like resolveProviders' output: two
// first-class providers and a keyless custom OpenAI-compatible endpoint
// addressed by its own driver name.
func testProviders() (config.Provider, []config.Provider) {
	primary := config.Provider{
		Name: "gemini", Driver: schemas.Gemini,
		Models: []string{"gemini-2.5-flash", "gemini-2.5-pro"},
		APIKey: "key", Tools: true,
	}
	secondary := config.Provider{
		Name: "groq", Driver: schemas.Groq, Models: []string{"llama-3.3-70b-versatile"},
		APIKey: "key", Tools: true,
	}
	local := config.Provider{
		Name: "my-vllm", Driver: schemas.ModelProvider("my-vllm"), Models: []string{"qwen3-32b"},
		BaseURL: "http://127.0.0.1:8000/v1", Tools: true, IsCustom: true, KeyLess: true,
	}
	return primary, []config.Provider{primary, secondary, local}
}

// The ladder spends the primary provider's other models before leaving the
// vendor, and never repeats the attempt that just failed.
func TestFallbacksForCoversSiblingModelsThenOtherProviders(t *testing.T) {
	primary, chain := testProviders()
	fallbacks := fallbacksFor(chain, primary, primary.Default())

	want := []schemas.Fallback{
		{Provider: schemas.Gemini, Model: "gemini-2.5-pro"},
		{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
		{Provider: schemas.ModelProvider("my-vllm"), Model: "qwen3-32b"},
	}
	if len(fallbacks) != len(want) {
		t.Fatalf("got %d fallbacks %v, want %d %v", len(fallbacks), fallbacks, len(want), want)
	}
	for i := range want {
		if fallbacks[i] != want[i] {
			t.Fatalf("fallback %d = %v, want %v", i, fallbacks[i], want[i])
		}
	}
}

// When the model being attempted is not the provider's default, the default
// becomes a fallback and the attempted pair is skipped.
func TestFallbacksForSkipsAttemptedModelOnly(t *testing.T) {
	primary, chain := testProviders()
	fallbacks := fallbacksFor(chain, primary, "gemini-2.5-pro")

	if len(fallbacks) == 0 {
		t.Fatal("expected fallbacks")
	}
	if got := fallbacks[0]; got.Model != "gemini-2.5-flash" {
		t.Fatalf("first fallback = %v, want the provider's default model", got)
	}
	for _, f := range fallbacks {
		if f.Provider == schemas.Gemini && f.Model == "gemini-2.5-pro" {
			t.Fatal("the attempted model appeared in its own fallback list")
		}
	}
}

// A model listed under two providers must appear once: a repeat only adds a
// round trip to every failure.
func TestFallbacksForDropsRepeats(t *testing.T) {
	primary := config.Provider{
		Name: "groq", Driver: schemas.Groq,
		Models: []string{"openai/gpt-oss-120b"},
		APIKey: "key", Tools: true,
	}
	duplicate := config.Provider{
		Name: "openrouter", Driver: schemas.OpenRouter,
		Models: []string{"openai/gpt-oss-120b", "meta/llama-3.3-70b"},
		APIKey: "key", Tools: true,
	}
	chain := []config.Provider{primary, duplicate}

	fallbacks := fallbacksFor(chain, primary, primary.Default())
	want := []schemas.Fallback{{Provider: schemas.OpenRouter, Model: "openai/gpt-oss-120b"}}
	if len(fallbacks) != 1 || fallbacks[0] != want[0] {
		t.Fatalf("fallbacks = %v, want a single %v", fallbacks, want)
	}
}

// A lone provider with a single model has nothing to fall back to.
func TestFallbacksForSoleProviderIsEmpty(t *testing.T) {
	primary := config.Provider{
		Name: "groq", Driver: schemas.Groq,
		Models: []string{"llama-3.3-70b-versatile"}, APIKey: "key", Tools: true,
	}
	if got := fallbacksFor([]config.Provider{primary}, primary, primary.Default()); len(got) != 0 {
		t.Fatalf("fallbacks = %v, want none", got)
	}
}

func TestProviderByDriver(t *testing.T) {
	primary, chain := testProviders()
	got, ok := providerByDriver(chain, string(primary.Driver))
	if !ok || got.Name != primary.Name {
		t.Fatalf("providerByDriver(%q) = %+v, %v", primary.Driver, got, ok)
	}
	if _, ok := providerByDriver(chain, "not-configured"); ok {
		t.Fatal("an unconfigured driver must not resolve")
	}
}

func TestIsPrivateHost(t *testing.T) {
	cases := map[string]bool{
		"http://127.0.0.1:8000/v1":       true,
		"http://localhost:11434/v1":      true,
		"http://192.168.1.10:8000/v1":    true,
		"http://10.0.0.5:8000/v1":        true,
		"https://api.example.com/v1":     false,
		"https://gateway.example.com/v1": false,
	}
	for raw, want := range cases {
		if got := isPrivateHost(raw); got != want {
			t.Fatalf("isPrivateHost(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestParseResponseToleratesMissingChoices(t *testing.T) {
	// Message is promoted from an embedded pointer, so it is nil in a
	// text/shape mismatch; parseResponse must guard before touching it.
	noMessage := &schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{
			{ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{}},
		},
	}
	for name, resp := range map[string]*schemas.BifrostChatResponse{
		"nil response":       nil,
		"no choices":         {},
		"choice without msg": noMessage,
	} {
		got := parseResponse(resp)
		if got.Text != "" || len(got.ToolCalls) != 0 {
			t.Fatalf("%s: parseResponse = %+v, want an empty response", name, got)
		}
	}
}

func TestParseResponseExtractsTextToolsAndRouting(t *testing.T) {
	resp := &schemas.BifrostChatResponse{
		Model: "top-level-model",
		Choices: []schemas.BifrostResponseChoice{
			{ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
				Message: &schemas.ChatMessage{
					Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("hello there")},
					ChatAssistantMessage: &schemas.ChatAssistantMessage{
						ToolCalls: []schemas.ChatAssistantMessageToolCall{{
							Type: schemas.Ptr("function"),
							ID:   schemas.Ptr("call_1"),
							Function: schemas.ChatAssistantMessageToolCallFunction{
								Name:      schemas.Ptr("remember"),
								Arguments: `{"content":"likes tea"}`,
							},
						}},
					},
				},
			}},
		},
		ExtraFields: schemas.BifrostResponseExtraFields{
			RoutingInfo: schemas.RoutingInfo{
				Provider:   schemas.Groq,
				Model:      "llama-3.3-70b-versatile",
				IsFallback: true,
			},
		},
	}

	got := parseResponse(resp)
	if got.Text != "hello there" {
		t.Fatalf("Text = %q, want %q", got.Text, "hello there")
	}
	if got.Provider != string(schemas.Groq) || got.Model != "llama-3.3-70b-versatile" || !got.IsFallback {
		t.Fatalf("routing = %q/%q/%v", got.Provider, got.Model, got.IsFallback)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(got.ToolCalls))
	}
	if tc := got.ToolCalls[0]; tc.ID != "call_1" || tc.Name != "remember" || tc.Arguments != `{"content":"likes tea"}` {
		t.Fatalf("tool call = %+v", tc)
	}
}

func TestParseResponseFallsBackToTopLevelModel(t *testing.T) {
	got := parseResponse(&schemas.BifrostChatResponse{Model: "gemini-2.5-flash"})
	if got.Model != "gemini-2.5-flash" {
		t.Fatalf("Model = %q, want the top-level model", got.Model)
	}
}

func TestChatErrorFormatting(t *testing.T) {
	withStatus := &ChatError{StatusCode: 400, Message: "bad tool schema"}
	if got := withStatus.Error(); !strings.Contains(got, "400") || !strings.Contains(got, "bad tool schema") {
		t.Fatalf("Error() = %q", got)
	}
	withoutStatus := &ChatError{Message: "dial timeout"}
	if got := withoutStatus.Error(); strings.Contains(got, "HTTP") {
		t.Fatalf("Error() = %q should not mention a status code", got)
	}
}
