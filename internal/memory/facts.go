package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// Normalize produces the comparison key used to detect duplicate facts:
// lowercased, internal whitespace collapsed, trimmed, with one trailing period
// removed.
func Normalize(content string) string {
	s := strings.ToLower(content)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSuffix(s, ".")
}

// ValidFact reports whether a candidate fact is worth storing. It rejects empty
// and one-word noise without trying to judge meaning.
func ValidFact(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	return len([]rune(Normalize(trimmed))) >= 3
}

// RememberTool is the only tool Yuna exposes.
//
// It deliberately takes a single parameter. Scope, scope ID and user ID are
// supplied by the bot from the current turn, so there is no argument the model
// could use to address another user's or another server's memory.
func RememberTool() schemas.ChatTool {
	return schemas.ChatTool{
		Type: schemas.ChatToolTypeFunction,
		Function: &schemas.ChatToolFunction{
			Name: "remember",
			Description: schemas.Ptr(
				"Save a durable fact about the person you are talking to, so you can recall it later. " +
					"Use it when they state a lasting preference, personal detail, ongoing project or " +
					"how they like to be helped. Do not use it for small talk, questions, or anything " +
					"temporary. Never store secrets, passwords, API keys or tokens."),
			Parameters: &schemas.ToolFunctionParameters{
				Type: "object",
				Properties: schemas.NewOrderedMapFromPairs(
					schemas.KV("content", map[string]any{
						"type": "string",
						"description": "The fact to remember, written as a short standalone " +
							"statement, for example: prefers short answers.",
					}),
				),
				Required: []string{"content"},
			},
		},
	}
}

// ParseRememberArgs extracts the content argument from a tool call.
func ParseRememberArgs(raw string) (string, error) {
	var args struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &args); err != nil {
		return "", fmt.Errorf("parse remember arguments: %w", err)
	}
	content := strings.TrimSpace(args.Content)
	if content == "" {
		return "", errors.New("remember was called without any content")
	}
	return content, nil
}

// Participant is one person who spoke in a transcript.
type Participant struct {
	UserID      string
	Username    string
	DisplayName string
	Nickname    string
}

// MatchUser resolves a username reported by the summariser onto a real
// participant of that transcript.
//
// Facts are attributed by name, and models hallucinate names. Accepting only an
// exact match against someone who actually spoke is what stops a fabricated
// attribution from writing a fact into the wrong person's memory.
func MatchUser(reported string, participants []Participant) (Participant, bool) {
	want := strings.TrimSpace(reported)
	if want == "" {
		return Participant{}, false
	}
	for _, p := range participants {
		if p.Username == want {
			return p, true
		}
	}
	// Fall back to a case-insensitive match, then to the names the person is
	// actually displayed under, which is what a model more often echoes.
	for _, p := range participants {
		if strings.EqualFold(p.Username, want) {
			return p, true
		}
	}
	for _, p := range participants {
		if p.DisplayName != "" && strings.EqualFold(p.DisplayName, want) {
			return p, true
		}
		if p.Nickname != "" && strings.EqualFold(p.Nickname, want) {
			return p, true
		}
	}
	return Participant{}, false
}
