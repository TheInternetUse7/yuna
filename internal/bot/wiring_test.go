package bot

import (
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/TheInternetUse7/yuna/internal/config"
)

// The gateway close codes are the difference between a clear message and a bot
// that "just does not start".
func TestGatewayHint(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"refused intents", errors.New("websocket: close 4014: Disallowed intent(s)"), "Privileged Gateway Intents"},
		{"bad token", errors.New("websocket: close 4004: Authentication failed"), "DISCORD_TOKEN"},
		{"unrelated", errors.New("dial tcp 1.2.3.4:443: connection refused"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gatewayHint(tc.err)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("gatewayHint = %q, want no hint", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("gatewayHint = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// Every command Commands() publishes must have a handler, or Discord shows a
// command that silently does nothing.
func TestEveryPublishedCommandIsDispatched(t *testing.T) {
	b := &Bot{}
	for _, c := range Commands() {
		if b.handlerFor(c.Name) == nil {
			t.Errorf("command %q is published but has no handler", c.Name)
		}
	}
	if b.handlerFor("not_a_command") != nil {
		t.Error("an unknown command resolved to a handler")
	}
}

// Message Content and Server Members are privileged intents. Dropping either
// makes the bot connect but see no message text, so pin the whole set.
func TestNewRequestsTheIntentsTheBotNeeds(t *testing.T) {
	b, err := New(&config.Config{DiscordToken: "test-token"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	required := map[string]discordgo.Intent{
		"guilds":          discordgo.IntentsGuilds,
		"guild messages":  discordgo.IntentsGuildMessages,
		"direct messages": discordgo.IntentsDirectMessages,
		"message content": discordgo.IntentsMessageContent,
		"guild members":   discordgo.IntentsGuildMembers,
	}
	for name, intent := range required {
		if b.session.Identify.Intents&intent != intent {
			t.Errorf("the %s intent is missing from Identify.Intents", name)
		}
	}
}

// Events arrive from a live connection, so a malformed or irrelevant one must
// be dropped rather than panic on the gateway goroutine.
func TestGatewayHandlersIgnoreIrrelevantEvents(t *testing.T) {
	b := &Bot{}

	b.onMessage(nil, nil)
	b.onMessage(nil, &discordgo.MessageCreate{})
	b.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{}})
	b.onMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		Author: &discordgo.User{ID: "bot-1", Bot: true},
	}})

	b.onInteraction(nil, nil)
	b.onInteraction(nil, &discordgo.InteractionCreate{})
	b.onInteraction(nil, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{Type: discordgo.InteractionPing},
	})
}
