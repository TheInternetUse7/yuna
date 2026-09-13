package memory

import (
	"strings"
	"testing"

	"github.com/TheInternetUse7/yuna/internal/store"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"collapses and lowercases", "  Likes   TEA  ", "likes tea"},
		{"strips one trailing period", "Lives in Nairobi.", "lives in nairobi"},
		{"strips only one period", "hmm..", "hmm."},
		{"newlines collapse too", "line one\nline two", "line one line two"},
		{"empty stays empty", "   ", ""},
		{"punctuation is preserved", "prefers Go, not Rust", "prefers go, not rust"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in); got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidFact(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"prefers short answers", true},
		{"tea", true},
		{"", false},
		{"   ", false},
		{"..", false},
		{"a", false},
	}
	for _, tc := range cases {
		if got := ValidFact(tc.in); got != tc.want {
			t.Fatalf("ValidFact(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseRememberArgs(t *testing.T) {
	got, err := ParseRememberArgs(`{"content":"prefers short answers"}`)
	if err != nil {
		t.Fatalf("ParseRememberArgs: %v", err)
	}
	if got != "prefers short answers" {
		t.Fatalf("content = %q", got)
	}

	if _, err := ParseRememberArgs(`{"content":"   "}`); err == nil {
		t.Fatal("empty content must be rejected")
	}
	if _, err := ParseRememberArgs(`not json`); err == nil {
		t.Fatal("malformed arguments must be rejected")
	}

	// The schema exposes only content, so a model cannot smuggle in a scope.
	if _, err := ParseRememberArgs(`{"content":"x","user_id":"someone-else"}`); err != nil {
		t.Fatalf("extra keys should be ignored, got %v", err)
	}
}

func TestMatchUser(t *testing.T) {
	participants := []Participant{
		{UserID: "u1", Username: "alice", DisplayName: "Alice Wonder", Nickname: "ali"},
		{UserID: "u2", Username: "bob", DisplayName: "Bob", Nickname: ""},
	}

	cases := []struct {
		name     string
		reported string
		wantID   string
		wantOK   bool
	}{
		{"exact username", "alice", "u1", true},
		{"case insensitive username", "ALICE", "u1", true},
		{"display name", "Alice Wonder", "u1", true},
		{"nickname", "ali", "u1", true},
		{"second participant", "bob", "u2", true},
		{"hallucinated user", "carol", "", false},
		{"empty", "  ", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := MatchUser(tc.reported, participants)
			if ok != tc.wantOK {
				t.Fatalf("MatchUser(%q) ok = %v, want %v", tc.reported, ok, tc.wantOK)
			}
			if ok && got.UserID != tc.wantID {
				t.Fatalf("MatchUser(%q) = %s, want %s", tc.reported, got.UserID, tc.wantID)
			}
		})
	}
}

func TestRememberDedupsAndAppliesCap(t *testing.T) {
	st := newMemoryTestStore(t)
	m := NewManager(st, nil, Settings{
		SystemPrompt: "persona", FactsPerUser: 3, FactsInject: 20, Enabled: true,
	}, nil)
	turn := Turn{GuildID: "g1", ChannelID: "c1", UserID: "u1", Username: "alice"}

	for _, fact := range []string{"likes tea", "LIKES   TEA.", "lives in Nairobi", "uses Neovim"} {
		if err := m.Remember(turn, fact, store.SourceCommand); err != nil {
			t.Fatalf("Remember(%q): %v", fact, err)
		}
	}
	facts, err := m.FactsFor(turn, 0)
	if err != nil {
		t.Fatalf("FactsFor: %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("stored %d facts, want 3 (duplicate collapsed by normalisation)", len(facts))
	}

	// Two more pushes the cap; the oldest must go first.
	if err := m.Remember(turn, "prefers Go", store.SourceCommand); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := m.Remember(turn, "timezone is EAT", store.SourceCommand); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	facts, err = m.FactsFor(turn, 0)
	if err != nil {
		t.Fatalf("FactsFor: %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("cap not enforced: %d facts", len(facts))
	}
	for _, f := range facts {
		if f.Content == "likes tea" || f.Content == "lives in Nairobi" {
			t.Fatalf("the oldest facts should have been evicted, found %q", f.Content)
		}
	}

	if err := m.Remember(turn, "..", store.SourceCommand); err == nil {
		t.Fatal("a fact that normalises away must be rejected")
	}
}

func TestFactsForIsScopedToTheCaller(t *testing.T) {
	st := newMemoryTestStore(t)
	m := NewManager(st, nil, Settings{SystemPrompt: "persona", FactsInject: 20, Enabled: true}, nil)

	alice := Turn{GuildID: "g1", ChannelID: "c1", UserID: "u1", Username: "alice"}
	bob := Turn{GuildID: "g1", ChannelID: "c1", UserID: "u2", Username: "bob"}

	if err := m.Remember(alice, "Alice likes tea", store.SourceCommand); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := m.Remember(bob, "Bob likes coffee", store.SourceCommand); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	bobFacts, err := m.FactsFor(bob, 0)
	if err != nil {
		t.Fatalf("FactsFor: %v", err)
	}
	if len(bobFacts) != 1 || bobFacts[0].Content != "Bob likes coffee" {
		t.Fatalf("FactsFor(bob) = %+v, want only Bob's fact", bobFacts)
	}
}

func TestForgetVariants(t *testing.T) {
	st := newMemoryTestStore(t)
	m := NewManager(st, nil, Settings{SystemPrompt: "persona", FactsInject: 20, Enabled: true}, nil)

	guild := Turn{GuildID: "g1", ChannelID: "c1", UserID: "u1", Username: "alice"}
	dm := Turn{ChannelID: "dm1", UserID: "u1", Username: "alice"}
	other := Turn{GuildID: "g1", ChannelID: "c1", UserID: "u2", Username: "bob"}

	if err := m.Remember(guild, "guild fact", store.SourceCommand); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := m.Remember(dm, "dm fact", store.SourceCommand); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := m.Remember(other, "bob fact", store.SourceCommand); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID: "m" + string(rune('a'+i)), ChannelID: "c1", GuildID: "g1",
			UserID: "u1", Username: "alice", Role: store.RoleUser, Content: "hi",
		}); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}
	if _, err := st.InsertMessage(store.StoredMessage{
		MessageID: "m-bob", ChannelID: "c1", GuildID: "g1",
		UserID: "u2", Username: "bob", Role: store.RoleUser, Content: "hi",
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Scope-only forget leaves the DM fact and everyone else's facts alone.
	facts, messages, err := m.Forget(guild, false, false)
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if facts != 1 || messages != 0 {
		t.Fatalf("Forget(scope) = %d facts, %d messages; want 1, 0", facts, messages)
	}
	if got, _ := m.FactsFor(dm, 0); len(got) != 1 {
		t.Fatalf("the DM fact was removed by a guild-scoped forget: %+v", got)
	}
	if got, _ := m.FactsFor(other, 0); len(got) != 1 {
		t.Fatalf("another user's fact was removed: %+v", got)
	}

	// include_history removes only the caller's messages in that channel.
	facts, messages, err = m.Forget(guild, false, true)
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if facts != 0 {
		t.Fatalf("Forget after the first one removed %d facts, want 0", facts)
	}
	if messages != 2 {
		t.Fatalf("Forget(include_history) removed %d messages, want 2", messages)
	}
	if remaining, err := st.RecentMessages("c1", 10); err != nil {
		t.Fatalf("RecentMessages: %v", err)
	} else if len(remaining) != 1 || remaining[0].UserID != "u2" {
		t.Fatalf("Bob's message should have survived, got %+v", remaining)
	}

	// all removes the caller's facts in every scope, including the DM.
	facts, _, err = m.Forget(guild, true, false)
	if err != nil {
		t.Fatalf("Forget(all): %v", err)
	}
	if facts != 1 {
		t.Fatalf("Forget(all) removed %d facts, want 1 (the DM one)", facts)
	}
	if got, _ := m.FactsFor(dm, 0); len(got) != 0 {
		t.Fatalf("DM facts survived Forget(all): %+v", got)
	}
}

func TestTranscriptAndParticipants(t *testing.T) {
	msgs := []store.StoredMessage{
		{UserID: "u1", Username: "alice", DisplayName: "Alice", Nickname: "ali",
			Role: store.RoleUser, Content: "hello\nthere"},
		{Role: store.RoleAssistant, Content: "hi"},
		{UserID: "u1", Username: "alice", Role: store.RoleUser, Content: "again"},
		{UserID: "u2", Username: "bob", DisplayName: "Bob", Role: store.RoleUser, Content: "hey"},
	}

	transcript := Transcript(msgs)
	if !strings.Contains(transcript, "alice: hello there") {
		t.Fatalf("transcript did not flatten newlines:\n%s", transcript)
	}
	if !strings.Contains(transcript, "Yuna: hi") {
		t.Fatalf("assistant lines should be attributed to Yuna:\n%s", transcript)
	}

	participants := Participants(msgs)
	if len(participants) != 2 {
		t.Fatalf("got %d participants, want 2 unique humans", len(participants))
	}
	if participants[0].Username != "alice" || participants[1].Username != "bob" {
		t.Fatalf("participants = %+v, want alice then bob", participants)
	}
}
