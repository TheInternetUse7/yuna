package bot

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/TheInternetUse7/yuna/internal/store"
)

const testBotID = "bot-1"

type msgOption func(*discordgo.Message)

func asDM(m *discordgo.Message) { m.GuildID = "" }

func fromBot(m *discordgo.Message) { m.Author.Bot = true }

func authoredBy(id string) msgOption {
	return func(m *discordgo.Message) { m.Author.ID = id }
}

func mentioning(id string) msgOption {
	return func(m *discordgo.Message) {
		m.Mentions = append(m.Mentions, &discordgo.User{ID: id})
	}
}

func replyingTo(id string) msgOption {
	return func(m *discordgo.Message) {
		m.MessageReference = &discordgo.MessageReference{MessageID: id}
	}
}

func newMessage(opts ...msgOption) *discordgo.Message {
	m := &discordgo.Message{
		ID:        "msg-1",
		ChannelID: "chan-1",
		GuildID:   "guild-1",
		Content:   "hello",
		Author:    &discordgo.User{ID: "user-1", Username: "alice"},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// TestShouldRespondAndStore is the trigger policy: when Yuna speaks and when she
// remembers. Both are checked together because they must never disagree.
func TestShouldRespondAndStore(t *testing.T) {
	cases := []struct {
		name        string
		msg         *discordgo.Message
		isAIChannel bool
		replyToBot  bool
		wantRespond bool
		wantStore   bool
	}{
		{"direct message", newMessage(asDM), false, false, true, true},
		{"direct message from another bot", newMessage(asDM, fromBot), false, false, false, false},
		{"ai channel", newMessage(), true, false, true, true},
		{"mention", newMessage(mentioning(testBotID)), false, false, true, true},
		{"reply that reaches yuna", newMessage(), false, true, true, true},
		{"plain message in a normal channel", newMessage(), false, false, false, false},
		{"plain message from a bot", newMessage(fromBot), false, false, false, false},
		{"bot message inside an ai channel", newMessage(fromBot), true, false, false, false},
		{"mention of somebody else", newMessage(mentioning("user-2")), false, false, false, false},
		{"yuna's own reply", newMessage(authoredBy(testBotID), fromBot), false, false, false, true},
		{"nil message", nil, true, true, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			respond := shouldRespond(tc.msg, testBotID, tc.isAIChannel, tc.replyToBot)
			if respond != tc.wantRespond {
				t.Errorf("shouldRespond = %v, want %v", respond, tc.wantRespond)
			}
			store := shouldStore(tc.msg, testBotID, tc.isAIChannel, tc.replyToBot)
			if store != tc.wantStore {
				t.Errorf("shouldStore = %v, want %v", store, tc.wantStore)
			}
			// The two policies must agree, or Yuna answers a message the next
			// turn cannot see.
			if respond && !store {
				t.Error("Yuna would answer this message without storing it")
			}
		})
	}
}

func TestMessageWithoutAuthorIsIgnored(t *testing.T) {
	msg := &discordgo.Message{ID: "msg-1", ChannelID: "chan-1", GuildID: "guild-1"}
	if shouldRespond(msg, testBotID, true, true) {
		t.Error("a message with no author must not be answered")
	}
	if shouldStore(msg, testBotID, true, true) {
		t.Error("a message with no author must not be stored")
	}
}

// The reason names the trigger that fired (which the log prints) or the one
// that was missing (which keeps the policy honest under test).
func TestClassifyNamesTheTrigger(t *testing.T) {
	cases := []struct {
		name        string
		msg         *discordgo.Message
		isAIChannel bool
		replyToBot  bool
		want        string
	}{
		{"mention", newMessage(mentioning(testBotID)), false, false, "mentions yuna"},
		{"ai channel", newMessage(), true, false, "ai channel"},
		{"reply", newMessage(), false, true, "reply reaches yuna"},
		{"direct message", newMessage(asDM), false, false, "direct message"},
		{"no trigger", newMessage(), false, false, "no mention, not an ai channel, not a reply"},
		{"another bot", newMessage(fromBot), false, false, "the author is a bot"},
		{"yuna's own message", newMessage(authoredBy(testBotID), fromBot), false, false, "yuna's own message"},
		{"no author", nil, false, false, "the message has no author"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.msg, testBotID, tc.isAIChannel, tc.replyToBot).reason; got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMentionsUser(t *testing.T) {
	if !mentionsUser(newMessage(mentioning(testBotID)), testBotID) {
		t.Fatal("a direct mention was not detected")
	}
	if mentionsUser(newMessage(), testBotID) {
		t.Fatal("a message with no mentions matched")
	}
	if mentionsUser(newMessage(mentioning("user-2")), testBotID) {
		t.Fatal("a mention of somebody else matched")
	}

	withNil := newMessage()
	withNil.Mentions = []*discordgo.User{nil, {ID: testBotID}}
	if !mentionsUser(withNil, testBotID) {
		t.Fatal("a nil mention entry must not hide a later mention")
	}
}

func TestReferencedID(t *testing.T) {
	if got := referencedID(nil); got != "" {
		t.Fatalf("referencedID(nil) = %q, want empty", got)
	}
	if got := referencedID(newMessage()); got != "" {
		t.Fatalf("referencedID(no reference) = %q, want empty", got)
	}
	if got := referencedID(newMessage(replyingTo("msg-0"))); got != "msg-0" {
		t.Fatalf("referencedID = %q, want msg-0", got)
	}
}

func TestIsBotAuthored(t *testing.T) {
	if isBotAuthored(nil, testBotID) {
		t.Fatal("a nil message must not be treated as the bot's")
	}
	if !isBotAuthored(newMessage(authoredBy(testBotID)), testBotID) {
		t.Fatal("the bot's own message was not recognised")
	}
	if isBotAuthored(newMessage(authoredBy("user-2")), testBotID) {
		t.Fatal("another user's message was treated as the bot's")
	}
}

// Walking the reply chain must find Yuna however deep the chain goes, and must
// stop rather than run forever. Every message here is stored, so the walk never
// falls back to a REST fetch.
func TestReplyChainReachesBot(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "yuna.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	// A fetcher that always fails: everything below must resolve from the store
	// alone, so any REST call is a bug in the walk.
	b := &Bot{store: st, fetcher: failingFetcher{}}
	insert := func(id, role, repliesTo string) {
		t.Helper()
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID:        id,
			ChannelID:        "chan-1",
			UserID:           "user-1",
			Username:         "alice",
			Role:             role,
			Content:          id,
			ReplyToMessageID: repliesTo,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	insert("yuna-1", store.RoleAssistant, "")
	insert("alice-1", store.RoleUser, "yuna-1")
	insert("alice-2", store.RoleUser, "alice-1")
	insert("alice-loose", store.RoleUser, "")

	cases := []struct {
		name    string
		replyTo string
		want    bool
	}{
		{"direct reply to yuna", "yuna-1", true},
		{"reply two links down the chain", "alice-2", true},
		{"reply to a message that answers nobody", "alice-loose", false},
		{"not a reply at all", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var msg *discordgo.Message
			if tc.replyTo == "" {
				msg = newMessage()
			} else {
				msg = newMessage(replyingTo(tc.replyTo))
			}
			if got := b.replyChainReachesBot("chan-1", msg); got != tc.want {
				t.Fatalf("replyChainReachesBot = %v, want %v", got, tc.want)
			}
		})
	}
}

// failingFetcher stands in for a Discord that does not have the message.
type failingFetcher struct{}

func (failingFetcher) ChannelMessage(_, _ string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	return nil, errors.New("no such message")
}

// stubFetcher serves messages the bot never stored, so the REST fallback of the
// reply-chain walk can be exercised without a gateway.
type stubFetcher struct {
	messages map[string]*discordgo.Message
	err      error
}

func (s stubFetcher) ChannelMessage(_, messageID string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if s.err != nil {
		return nil, s.err
	}
	msg, ok := s.messages[messageID]
	if !ok {
		return nil, errors.New("no such message")
	}
	return msg, nil
}

func messageFrom(id, authorID, repliesTo string) *discordgo.Message {
	msg := &discordgo.Message{ID: id, Author: &discordgo.User{ID: authorID}}
	if repliesTo != "" {
		msg.MessageReference = &discordgo.MessageReference{MessageID: repliesTo}
	}
	return msg
}

// Nothing in the database: the walk has to fetch each link from Discord.
func TestReplyChainFallsBackToDiscord(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "yuna.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	b := &Bot{
		store: st,
		appID: testBotID,
		fetcher: stubFetcher{messages: map[string]*discordgo.Message{
			"yuna-1":  messageFrom("yuna-1", testBotID, ""),
			"alice-1": messageFrom("alice-1", "user-2", "yuna-1"),
			"alice-2": messageFrom("alice-2", "user-2", "alice-1"),
			"alice-3": messageFrom("alice-3", "user-2", ""),
		}},
	}

	if !b.replyChainReachesBot("chan-1", newMessage(replyingTo("yuna-1"))) {
		t.Error("a fetched reply to yuna was not recognised")
	}
	if !b.replyChainReachesBot("chan-1", newMessage(replyingTo("alice-2"))) {
		t.Error("a fetched chain two links down was not walked")
	}
	if b.replyChainReachesBot("chan-1", newMessage(replyingTo("alice-3"))) {
		t.Error("a fetched chain that never reaches yuna was reported as reaching her")
	}

	broken := &Bot{store: st, appID: testBotID, fetcher: stubFetcher{err: errors.New("gateway down")}}
	if broken.replyChainReachesBot("chan-1", newMessage(replyingTo("yuna-1"))) {
		t.Error("a failed fetch must not be treated as reaching yuna")
	}
}

func TestReplyChainIsBounded(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "yuna.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	b := &Bot{store: st, fetcher: failingFetcher{}}
	insert := func(id, repliesTo string) {
		t.Helper()
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID:        id,
			ChannelID:        "chan-1",
			UserID:           "user-1",
			Username:         "alice",
			Role:             store.RoleUser,
			Content:          id,
			ReplyToMessageID: repliesTo,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	// A chain longer than replyChainLimit that ends at one of Yuna's messages.
	insert("link-0", "")
	if _, err := st.InsertMessage(store.StoredMessage{
		MessageID: "yuna-1", ChannelID: "chan-1", UserID: "bot-1",
		Username: "yuna", Role: store.RoleAssistant, Content: "hi",
	}); err != nil {
		t.Fatalf("insert yuna: %v", err)
	}
	previous := "yuna-1"
	for i := 1; i <= replyChainLimit+3; i++ {
		id := fmt.Sprintf("link-%d", i)
		insert(id, previous)
		previous = id
	}

	if b.replyChainReachesBot("chan-1", newMessage(replyingTo(previous))) {
		t.Fatal("a chain longer than the limit must not be walked to the end")
	}
}
