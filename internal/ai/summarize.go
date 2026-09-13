package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/config"
)

// ExtractedFact is one fact the summariser attributed to a named user.
type ExtractedFact struct {
	User string
	Fact string
}

// SummaryResult is the parsed output of a summarisation pass.
type SummaryResult struct {
	Summary string
	Facts   []ExtractedFact
}

const summarySystemPrompt = `You compress Discord conversations for a chatbot's long-term memory.

You receive a transcript where each line is prefixed with the speaker's username.

Return ONE JSON object and nothing else, in exactly this shape:
{"summary": "...", "facts": [{"user": "<username>", "fact": "..."}]}

Rules for "summary":
- 2 to 6 sentences covering what was discussed, decided or asked.
- Written in the third person, no greetings, no filler.
- Preserve concrete details: names, numbers, preferences, decisions, open questions.

Rules for "facts":
- Only durable, reusable statements about a specific person, e.g. their
  preferences, timezone, role, ongoing projects, or how they like to be helped.
- "user" MUST be copied verbatim from a username that appears in the transcript.
  Never invent, translate or guess a username.
- Never record secrets: no API keys, tokens, passwords or private credentials.
- Skip small talk, questions that were answered, and anything temporary.
- Return [] when there is nothing worth remembering.`

// Summarize produces a rolling summary plus extracted facts for one channel's
// transcript. The transcript must already be rendered with author names; the
// caller validates that every extracted username really appears in it.
func (c *Client) Summarize(ctx context.Context, provider config.Provider, chain []config.Provider,
	transcript string) (*SummaryResult, error) {
	temperature := 0.0
	msgs := []schemas.ChatMessage{
		{
			Role:    schemas.ChatMessageRoleSystem,
			Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(summarySystemPrompt)},
		},
		{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(transcript)},
		},
	}

	resp, err := c.do(ctx, request{
		primary:     provider,
		fallbacks:   fallbacksFor(chain, provider),
		messages:    msgs,
		temperature: &temperature,
	})
	if err != nil {
		return nil, err
	}
	return ParseSummaryJSON(resp.Text)
}

// ParseSummaryJSON reads the summariser's reply leniently: models routinely wrap
// JSON in markdown fences or add a sentence before it.
func ParseSummaryJSON(raw string) (*SummaryResult, error) {
	body := extractJSONObject(raw)
	if body == "" {
		return nil, fmt.Errorf("summariser returned no JSON object")
	}

	var payload struct {
		Summary string `json:"summary"`
		Facts   []struct {
			User string `json:"user"`
			Fact string `json:"fact"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, fmt.Errorf("parse summariser JSON: %w", err)
	}

	result := &SummaryResult{Summary: strings.TrimSpace(payload.Summary)}
	for _, f := range payload.Facts {
		user := strings.TrimSpace(f.User)
		fact := strings.TrimSpace(f.Fact)
		if user == "" || fact == "" {
			continue
		}
		result.Facts = append(result.Facts, ExtractedFact{User: user, Fact: fact})
	}
	return result, nil
}

// extractJSONObject strips a markdown fence if present, then returns the
// outermost {...} span.
func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			rest = rest[:j]
		}
		s = strings.TrimSpace(rest)
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}
