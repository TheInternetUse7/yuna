package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/applog"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// maxTranscriptMessages caps how much history one summary refresh may read, so
// a channel that has been quiet for months cannot produce a giant request.
const maxTranscriptMessages = 200

// summaryTimeout bounds a background summarisation call.
const summaryTimeout = 90 * time.Second

// Settings tunes how much memory is gathered and injected.
type Settings struct {
	SystemPrompt    string
	HistoryWindow   int
	SummaryEvery    int
	SummaryProvider config.Provider
	Chain           []config.Provider
	FactsPerUser    int
	FactsInject     int
	Enabled         bool
}

// Summarizer is the single AI capability memory needs. *ai.Client satisfies it;
// tests inject a fake.
type Summarizer interface {
	Summarize(ctx context.Context, provider config.Provider, chain []config.Provider,
		transcript string) (*ai.SummaryResult, error)
}

// Manager is the bot's only entry point to memory.
type Manager struct {
	store      *store.Store
	summarizer Summarizer
	settings   Settings
	log        *applog.Logger

	mu          sync.Mutex
	summarising map[string]bool
}

// NewManager wires the memory layer.
func NewManager(st *store.Store, summarizer Summarizer, settings Settings, log *applog.Logger) *Manager {
	return &Manager{
		store:       st,
		summarizer:  summarizer,
		settings:    settings,
		log:         log,
		summarising: make(map[string]bool),
	}
}

// Context assembles the message list for one turn. It is the only path by which
// remembered content can reach a model.
func (m *Manager) Context(turn Turn) ([]schemas.ChatMessage, error) {
	window, err := m.store.RecentMessages(turn.ChannelID, m.settings.HistoryWindow)
	if err != nil {
		return nil, err
	}

	var (
		facts   []store.Fact
		summary *store.ChannelSummary
	)
	if m.settings.Enabled {
		scope := turn.Scope()
		found, err := m.store.Facts(scope, turn.UserID, m.settings.FactsInject)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			// The query already filters on scope and author; re-checking keeps
			// the invariant true even if a query is ever changed carelessly.
			if Belongs(f, scope, turn.UserID) {
				facts = append(facts, f)
			}
		}
		if summary, err = m.store.Summary(turn.ChannelID); err != nil {
			return nil, err
		}
	}

	return BuildMessages(m.settings.SystemPrompt, turn, facts, summary, window), nil
}

// BuildMessages renders the exact list sent to the model:
//
//  1. one system message carrying the persona, the current author's facts and
//     the channel summary;
//  2. the recent window, oldest first.
//
// Nothing else is ever included.
func BuildMessages(systemPrompt string, turn Turn, facts []store.Fact,
	summary *store.ChannelSummary, window []store.StoredMessage) []schemas.ChatMessage {
	system := systemPrompt

	if len(facts) > 0 {
		var b strings.Builder
		b.WriteString("\n\nWhat you remember about ")
		b.WriteString(turn.DisplayLabel())
		b.WriteString(":")
		for _, f := range facts {
			b.WriteString("\n- ")
			b.WriteString(f.Content)
		}
		system += b.String()
	}
	if summary != nil && strings.TrimSpace(summary.Summary) != "" {
		system += "\n\nSummary of the earlier conversation in this channel:\n" +
			strings.TrimSpace(summary.Summary)
	}

	msgs := make([]schemas.ChatMessage, 0, len(window)+1)
	msgs = append(msgs, schemas.ChatMessage{
		Role:    schemas.ChatMessageRoleSystem,
		Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(system)},
	})

	for _, m := range window {
		if m.Role == store.RoleAssistant {
			msgs = append(msgs, schemas.ChatMessage{
				Role:    schemas.ChatMessageRoleAssistant,
				Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(m.Content)},
			})
			continue
		}
		msgs = append(msgs, schemas.ChatMessage{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(FormatUserMessage(m))},
		})
	}
	return msgs
}

// FormatUserMessage reproduces the original bot's user-message format, which
// gives the model the author's three names.
func FormatUserMessage(m store.StoredMessage) string {
	return fmt.Sprintf("User '%s' (Display Name: '%s', Nickname: '%s') says: %s",
		m.Username, m.DisplayName, m.Nickname, m.Content)
}

// DisplayLabel is how Yuna refers to the speaker in the prompt.
func (t Turn) DisplayLabel() string {
	if t.DisplayName != "" {
		return t.DisplayName
	}
	return t.Username
}

// Transcript renders stored messages for the summariser, one line per message,
// prefixed with the speaker so extracted facts can be attributed.
func Transcript(msgs []store.StoredMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		name := m.Username
		if m.Role == store.RoleAssistant {
			name = "Yuna"
		}
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(strings.Join(strings.Fields(m.Content), " "))
		b.WriteString("\n")
	}
	return b.String()
}

// Participants lists the humans who spoke, in first-seen order.
func Participants(msgs []store.StoredMessage) []Participant {
	seen := make(map[string]bool)
	var out []Participant
	for _, m := range msgs {
		if m.Role != store.RoleUser || m.UserID == "" || seen[m.UserID] {
			continue
		}
		seen[m.UserID] = true
		out = append(out, Participant{
			UserID:      m.UserID,
			Username:    m.Username,
			DisplayName: m.DisplayName,
			Nickname:    m.Nickname,
		})
	}
	return out
}

// Remember stores a fact about the turn's author in the turn's scope and trims
// that user's oldest facts when they exceed the cap.
func (m *Manager) Remember(turn Turn, content, source string) error {
	content = strings.TrimSpace(content)
	if !ValidFact(content) {
		return fmt.Errorf("nothing worth remembering in %q", content)
	}
	scope := turn.Scope()
	err := m.store.InsertFact(store.Fact{
		ScopeType:   scope.Type,
		ScopeID:     scope.ID,
		UserID:      turn.UserID,
		Content:     content,
		ContentNorm: Normalize(content),
		Source:      source,
	})
	if err != nil {
		return err
	}
	return m.enforceCap(scope, turn.UserID)
}

func (m *Manager) enforceCap(scope Scope, userID string) error {
	if m.settings.FactsPerUser <= 0 {
		return nil
	}
	count, err := m.store.CountFacts(scope, userID)
	if err != nil {
		return err
	}
	if count <= m.settings.FactsPerUser {
		return nil
	}
	return m.store.EvictOldestFacts(scope, userID, m.settings.FactsPerUser)
}

// FactsFor lists the turn author's facts in the turn's scope, newest first.
func (m *Manager) FactsFor(turn Turn, limit int) ([]store.Fact, error) {
	scope := turn.Scope()
	found, err := m.store.Facts(scope, turn.UserID, limit)
	if err != nil {
		return nil, err
	}
	kept := make([]store.Fact, 0, len(found))
	for _, f := range found {
		if Belongs(f, scope, turn.UserID) {
			kept = append(kept, f)
		}
	}
	return kept, nil
}

// Forget removes the caller's facts. all removes them in every scope;
// includeHistory also removes their stored messages from the current channel.
func (m *Manager) Forget(turn Turn, all, includeHistory bool) (facts, messages int64, err error) {
	facts, err = m.store.DeleteFacts(turn.Scope(), turn.UserID, all)
	if err != nil {
		return 0, 0, err
	}
	if includeHistory {
		messages, err = m.store.DeleteUserMessages(turn.ChannelID, turn.UserID)
		if err != nil {
			return facts, 0, err
		}
	}
	return facts, messages, nil
}

// MaybeSummarize refreshes a channel's summary once enough new messages have
// accumulated. It runs in the background and never blocks a reply.
func (m *Manager) MaybeSummarize(channelID, guildID string) {
	if !m.settings.Enabled || m.summarizer == nil {
		return
	}
	if !m.claim(channelID) {
		return
	}

	go func() {
		defer m.release(channelID)
		ctx, cancel := context.WithTimeout(context.Background(), summaryTimeout)
		defer cancel()
		if err := m.refreshSummary(ctx, channelID, guildID); err != nil {
			m.log.Warnf("summary refresh for channel %s failed: %v", channelID, err)
		}
	}()
}

func (m *Manager) claim(channelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.summarising[channelID] {
		return false
	}
	m.summarising[channelID] = true
	return true
}

func (m *Manager) release(channelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.summarising, channelID)
}

func (m *Manager) refreshSummary(ctx context.Context, channelID, guildID string) error {
	current, err := m.store.Summary(channelID)
	if err != nil {
		return err
	}
	var covered int64
	if current != nil {
		covered = current.CoversThroughMessageID
	}

	pending, err := m.store.CountMessagesSince(channelID, covered)
	if err != nil {
		return err
	}
	if pending < m.settings.SummaryEvery {
		return nil
	}

	msgs, err := m.store.MessagesSince(channelID, covered)
	if err != nil {
		return err
	}
	if len(msgs) > maxTranscriptMessages {
		msgs = msgs[:maxTranscriptMessages]
	}
	if len(msgs) == 0 {
		return nil
	}

	result, err := m.summarizer.Summarize(ctx, m.settings.SummaryProvider, m.settings.Chain, Transcript(msgs))
	if err != nil {
		// Leave covers_through_message_id untouched so the next trigger retries.
		return err
	}

	scope := DMScope(channelID)
	if guildID != "" {
		scope = GuildScope(guildID)
	}
	participants := Participants(msgs)
	touched := make(map[string]bool)
	for _, extracted := range result.Facts {
		person, ok := MatchUser(extracted.User, participants)
		if !ok {
			m.log.Debugf("dropping summary fact attributed to unknown user %q", extracted.User)
			continue
		}
		if !ValidFact(extracted.Fact) {
			continue
		}
		err := m.store.InsertFact(store.Fact{
			ScopeType:   scope.Type,
			ScopeID:     scope.ID,
			UserID:      person.UserID,
			Content:     extracted.Fact,
			ContentNorm: Normalize(extracted.Fact),
			Source:      store.SourceSummary,
		})
		if err != nil {
			return err
		}
		touched[person.UserID] = true
	}
	for userID := range touched {
		if err := m.enforceCap(scope, userID); err != nil {
			return err
		}
	}

	text := strings.TrimSpace(result.Summary)
	if text == "" {
		if current == nil {
			return nil
		}
		text = current.Summary
	}
	return m.store.UpsertSummary(store.ChannelSummary{
		ChannelID:              channelID,
		GuildID:                guildID,
		Summary:                text,
		CoversThroughMessageID: msgs[len(msgs)-1].ID,
	})
}
