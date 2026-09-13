package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// fakeSummarizer stands in for the model so summary tests never touch a network.
type fakeSummarizer struct {
	result     *ai.SummaryResult
	err        error
	calls      int
	transcript string
	model      string
}

func (f *fakeSummarizer) Summarize(_ context.Context, _ config.Provider, _ []config.Provider,
	model, transcript string) (*ai.SummaryResult, error) {
	f.calls++
	f.model = model
	f.transcript = transcript
	return f.result, f.err
}

func newMemoryTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func testManager(t *testing.T, st *store.Store, summarizer Summarizer) *Manager {
	t.Helper()
	return NewManager(st, summarizer, Settings{
		SystemPrompt:  "persona",
		HistoryWindow: 15,
		SummaryEvery:  3,
		FactsPerUser:  50,
		FactsInject:   20,
		Enabled:       true,
	}, nil)
}

func systemText(t *testing.T, msgs []schemas.ChatMessage) string {
	t.Helper()
	if len(msgs) == 0 {
		t.Fatal("no messages were built")
	}
	if msgs[0].Role != schemas.ChatMessageRoleSystem {
		t.Fatalf("first message role = %q, want system", msgs[0].Role)
	}
	if msgs[0].Content == nil || msgs[0].Content.ContentStr == nil {
		t.Fatal("system message has no text")
	}
	return *msgs[0].Content.ContentStr
}

func mustContext(t *testing.T, m *Manager, turn Turn) []schemas.ChatMessage {
	t.Helper()
	msgs, err := m.Context(turn)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	return msgs
}

// TestFactIsolationAcrossScopesAndUsers is the core privacy test. Whatever the
// prompt is, it must never contain another person's facts, or facts from a
// different guild or DM.
func TestFactIsolationAcrossScopesAndUsers(t *testing.T) {
	st := newMemoryTestStore(t)
	m := testManager(t, st, nil)

	guildA := GuildScope("guild-a")
	guildB := GuildScope("guild-b")
	dm := DMScope("dm-1")

	seed := func(scope Scope, userID, content string) {
		t.Helper()
		if err := st.InsertFact(store.Fact{
			ScopeType: scope.Type, ScopeID: scope.ID, UserID: userID,
			Content: content, ContentNorm: Normalize(content), Source: store.SourceCommand,
		}); err != nil {
			t.Fatalf("seed %q: %v", content, err)
		}
	}

	seed(guildA, "user-a", "Alice likes tea")
	seed(guildA, "user-b", "Bob likes coffee")
	seed(guildB, "user-a", "Alice plays chess in the other server")
	seed(dm, "user-a", "Alice is planning a surprise party")

	aliceTurn := Turn{
		GuildID: "guild-a", ChannelID: "chan-a",
		UserID: "user-a", Username: "alice", DisplayName: "Alice",
	}
	system := systemText(t, mustContext(t, m, aliceTurn))
	if !strings.Contains(system, "Alice likes tea") {
		t.Fatalf("Alice's own fact is missing from her prompt:\n%s", system)
	}
	for _, leak := range []string{
		"Bob likes coffee",
		"Alice plays chess in the other server",
		"Alice is planning a surprise party",
	} {
		if strings.Contains(system, leak) {
			t.Fatalf("leaked %q into Alice's prompt:\n%s", leak, system)
		}
	}

	// Bob, in the same channel and scope, must not see Alice's fact.
	bobTurn := Turn{
		GuildID: "guild-a", ChannelID: "chan-a",
		UserID: "user-b", Username: "bob", DisplayName: "Bob",
	}
	system = systemText(t, mustContext(t, m, bobTurn))
	if strings.Contains(system, "Alice likes tea") {
		t.Fatalf("leaked Alice's fact into Bob's prompt:\n%s", system)
	}
	if !strings.Contains(system, "Bob likes coffee") {
		t.Fatalf("Bob's own fact is missing:\n%s", system)
	}

	// The DM scope must not see anything said in a guild, and vice versa.
	dmTurn := Turn{ChannelID: "dm-1", UserID: "user-a", Username: "alice", DisplayName: "Alice"}
	system = systemText(t, mustContext(t, m, dmTurn))
	if !strings.Contains(system, "Alice is planning a surprise party") {
		t.Fatalf("Alice's DM fact is missing:\n%s", system)
	}
	if strings.Contains(system, "Alice likes tea") {
		t.Fatalf("a guild fact leaked into the DM:\n%s", system)
	}
}

func TestBelongs(t *testing.T) {
	scope := GuildScope("guild-a")
	cases := []struct {
		name   string
		fact   store.Fact
		userID string
		want   bool
	}{
		{"same scope and user", store.Fact{ScopeType: store.ScopeGuild, ScopeID: "guild-a", UserID: "u1"}, "u1", true},
		{"other user", store.Fact{ScopeType: store.ScopeGuild, ScopeID: "guild-a", UserID: "u2"}, "u1", false},
		{"other guild", store.Fact{ScopeType: store.ScopeGuild, ScopeID: "guild-b", UserID: "u1"}, "u1", false},
		{"other scope kind", store.Fact{ScopeType: store.ScopeDM, ScopeID: "guild-a", UserID: "u1"}, "u1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Belongs(tc.fact, scope, tc.userID); got != tc.want {
				t.Fatalf("Belongs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTurnScope(t *testing.T) {
	if got := (Turn{GuildID: "g1", ChannelID: "c1"}).Scope(); got != GuildScope("g1") {
		t.Fatalf("guild turn scope = %+v, want guild g1", got)
	}
	if got := (Turn{ChannelID: "dm1"}).Scope(); got != DMScope("dm1") {
		t.Fatalf("DM turn scope = %+v, want dm dm1", got)
	}
}

func TestBuildMessagesOrderAndAuthorFormat(t *testing.T) {
	window := []store.StoredMessage{
		{Username: "alice", DisplayName: "Alice", Nickname: "ali", Role: store.RoleUser, Content: "hi"},
		{Role: store.RoleAssistant, Content: "hello"},
	}
	facts := []store.Fact{{Content: "prefers short answers"}, {Content: "lives in Nairobi"}}
	summary := &store.ChannelSummary{Summary: "they discussed tea"}
	turn := Turn{Username: "alice", DisplayName: "Alice", Nickname: "ali"}

	msgs := BuildMessages("PERSONA", turn, facts, summary, window)
	if len(msgs) != 3 { // one system message plus the two window messages
		t.Fatalf("built %d messages, want 3", len(msgs))
	}

	system := systemText(t, msgs)
	persona := strings.Index(system, "PERSONA")
	factAt := strings.Index(system, "prefers short answers")
	summaryAt := strings.Index(system, "they discussed tea")
	switch {
	case persona < 0 || factAt < 0 || summaryAt < 0:
		t.Fatalf("system message is missing a section:\n%s", system)
	case !(persona < factAt && factAt < summaryAt):
		t.Fatalf("system sections are out of order (persona=%d facts=%d summary=%d):\n%s",
			persona, factAt, summaryAt, system)
	}
	if !strings.Contains(system, "What you remember about Alice:") {
		t.Fatalf("facts header is wrong:\n%s", system)
	}

	if msgs[1].Role != schemas.ChatMessageRoleUser {
		t.Fatalf("window[0] role = %q, want user", msgs[1].Role)
	}
	want := "User 'alice' (Display Name: 'Alice', Nickname: 'ali') says: hi"
	if got := *msgs[1].Content.ContentStr; got != want {
		t.Fatalf("user message format changed:\n got %q\nwant %q", got, want)
	}
	if msgs[2].Role != schemas.ChatMessageRoleAssistant {
		t.Fatalf("window[1] role = %q, want assistant", msgs[2].Role)
	}
	if got := *msgs[2].Content.ContentStr; got != "hello" {
		t.Fatalf("assistant content = %q, want %q", got, "hello")
	}
}

func TestBuildMessagesOmitsEmptySections(t *testing.T) {
	msgs := BuildMessages("PERSONA", Turn{}, nil, &store.ChannelSummary{Summary: "   "}, nil)
	if len(msgs) != 1 {
		t.Fatalf("built %d messages, want just the system one", len(msgs))
	}
	if got := systemText(t, msgs); got != "PERSONA" {
		t.Fatalf("system text = %q, want the bare persona", got)
	}
}

func TestContextRespectsHistoryWindow(t *testing.T) {
	st := newMemoryTestStore(t)
	m := NewManager(st, nil, Settings{
		SystemPrompt: "persona", HistoryWindow: 2, FactsInject: 20, Enabled: true,
	}, nil)

	for i, content := range []string{"one", "two", "three", "four"} {
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID: "m" + content, ChannelID: "chan-1", UserID: "u1",
			Username: "alice", DisplayName: "Alice", Nickname: "ali",
			Role: store.RoleUser, Content: content, CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	msgs := mustContext(t, m, Turn{ChannelID: "chan-1", UserID: "u1", Username: "alice"})
	if len(msgs) != 3 {
		t.Fatalf("built %d messages, want 3", len(msgs))
	}
	if got := *msgs[1].Content.ContentStr; !strings.HasSuffix(got, "says: three") {
		t.Fatalf("window did not keep the newest messages: %q", got)
	}
	if got := *msgs[2].Content.ContentStr; !strings.HasSuffix(got, "says: four") {
		t.Fatalf("window not in chronological order: %q", got)
	}
}

func TestContextOmitsMemoryWhenDisabled(t *testing.T) {
	st := newMemoryTestStore(t)
	if err := st.InsertFact(store.Fact{
		ScopeType: store.ScopeGuild, ScopeID: "g1", UserID: "u1",
		Content: "likes tea", ContentNorm: "likes tea", Source: store.SourceCommand,
	}); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}
	m := NewManager(st, nil, Settings{SystemPrompt: "persona", HistoryWindow: 5, Enabled: false}, nil)

	msgs := mustContext(t, m, Turn{GuildID: "g1", ChannelID: "c1", UserID: "u1"})
	if got := systemText(t, msgs); strings.Contains(got, "likes tea") {
		t.Fatalf("memory was injected while disabled:\n%s", got)
	}
}

func TestRefreshSummaryExtractsAndValidatesFacts(t *testing.T) {
	st := newMemoryTestStore(t)
	fake := &fakeSummarizer{result: &ai.SummaryResult{
		Summary: "Alice asked about deploy scripts.",
		Facts: []ai.ExtractedFact{
			{User: "alice", Fact: "prefers short answers"},
			{User: "ghost", Fact: "was never in this channel"},
			{User: "alice", Fact: ""},
		},
	}}
	m := testManager(t, st, fake)

	for i := 0; i < 3; i++ {
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID: "m" + string(rune('a'+i)), ChannelID: "chan-1", GuildID: "guild-1",
			UserID: "user-a", Username: "alice", DisplayName: "Alice", Nickname: "ali",
			Role: store.RoleUser, Content: "question", CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	if err := m.refreshSummary(context.Background(), "chan-1", "guild-1"); err != nil {
		t.Fatalf("refreshSummary: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("summarizer called %d times, want 1", fake.calls)
	}
	if !strings.Contains(fake.transcript, "alice: question") {
		t.Fatalf("transcript is not attributed by username:\n%s", fake.transcript)
	}

	summary, err := st.Summary("chan-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary == nil || summary.Summary != "Alice asked about deploy scripts." {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.CoversThroughMessageID == 0 {
		t.Fatal("covers_through_message_id was not advanced")
	}

	facts, err := st.Facts(GuildScope("guild-1"), "user-a", 0)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if len(facts) != 1 || facts[0].Content != "prefers short answers" {
		t.Fatalf("facts = %+v; the unknown user and the empty fact must be dropped", facts)
	}
	if facts[0].Source != store.SourceSummary {
		t.Fatalf("fact source = %q, want %q", facts[0].Source, store.SourceSummary)
	}
}

func TestRefreshSummarySkipsWhenBelowThreshold(t *testing.T) {
	st := newMemoryTestStore(t)
	fake := &fakeSummarizer{result: &ai.SummaryResult{Summary: "unused"}}
	m := testManager(t, st, fake)

	// SummaryEvery is 3; store only 2 messages.
	for i := 0; i < 2; i++ {
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID: "m" + string(rune('a'+i)), ChannelID: "chan-1",
			UserID: "user-a", Username: "alice", Role: store.RoleUser,
			Content: "question", CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}
	if err := m.refreshSummary(context.Background(), "chan-1", "guild-1"); err != nil {
		t.Fatalf("refreshSummary: %v", err)
	}
	if fake.calls != 0 {
		t.Fatalf("summarizer called %d times, want 0", fake.calls)
	}
}

func TestRefreshSummaryKeepsCursorOnFailure(t *testing.T) {
	st := newMemoryTestStore(t)
	fake := &fakeSummarizer{err: errors.New("provider down")}
	m := testManager(t, st, fake)

	for i := 0; i < 3; i++ {
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID: "m" + string(rune('a'+i)), ChannelID: "chan-1",
			UserID: "user-a", Username: "alice", Role: store.RoleUser,
			Content: "question", CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}
	if err := m.refreshSummary(context.Background(), "chan-1", "guild-1"); err == nil {
		t.Fatal("expected the summariser error to surface")
	}
	// The cursor must not move, so the next trigger retries the same range.
	if summary, _ := st.Summary("chan-1"); summary != nil {
		t.Fatalf("a summary was written despite the failure: %+v", summary)
	}
}
