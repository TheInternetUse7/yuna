// Package bot connects Discord to the model and to memory.
package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/applog"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/memory"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// Chat is the one AI capability the bot needs. *ai.Client satisfies it; tests
// inject a fake so no network call is ever made.
type Chat interface {
	ChatWithTools(ctx context.Context, primary config.Provider, chain []config.Provider,
		msgs []schemas.ChatMessage, tools []schemas.ChatTool, exec ai.ExecFunc) (*ai.Response, error)
}

// messageFetcher is the slice of the Discord REST API the reply-chain walk
// needs. *discordgo.Session satisfies it; tests inject a stub so a chain that
// leaves the database can be exercised without a live gateway.
type messageFetcher interface {
	ChannelMessage(channelID, messageID string, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// Bot is the running Discord client.
type Bot struct {
	cfg     *config.Config
	session *discordgo.Session
	fetcher messageFetcher
	store   *store.Store
	memory  *memory.Manager
	chat    Chat
	log     *applog.Logger

	appID string

	queue *queue
}

// New builds the session and registers handlers. Nothing connects until Run.
func New(cfg *config.Config, st *store.Store, mem *memory.Manager, chat Chat, log *applog.Logger) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	// Message Content and Server Members are privileged intents and must also be
	// enabled for the application in the Discord Developer Portal.
	session.Identify.Intents = discordgo.MakeIntent(
		discordgo.IntentsGuilds |
			discordgo.IntentsGuildMessages |
			discordgo.IntentsDirectMessages |
			discordgo.IntentsMessageContent |
			discordgo.IntentsGuildMembers,
	)

	b := &Bot{
		cfg:     cfg,
		session: session,
		fetcher: session,
		store:   st,
		memory:  mem,
		chat:    chat,
		log:     log,
	}
	b.queue = newQueue(b.generate)
	b.queue.onPanic = func(msg *discordgo.Message, r any) {
		b.log.Errorf("panic while generating reply in channel %s: %v", msg.ChannelID, r)
	}
	session.AddHandler(b.onMessage)
	session.AddHandler(b.onInteraction)
	return b, nil
}

// Run connects, publishes the slash commands and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open gateway: %w%s", err, gatewayHint(err))
	}
	defer b.session.Close()

	if b.session.State == nil || b.session.State.User == nil {
		return errors.New("gateway connected without a user; cannot register commands")
	}
	b.appID = b.session.State.User.ID

	commands := Commands()
	if _, err := b.session.ApplicationCommandBulkOverwrite(b.appID, b.cfg.GuildID, commands); err != nil {
		return fmt.Errorf("register slash commands: %w", err)
	}
	if b.cfg.GuildID == "" {
		b.log.Infof("registered %d global commands (global commands can take up to an hour to appear)",
			len(commands))
	} else {
		b.log.Infof("registered %d commands on guild %s", len(commands), b.cfg.GuildID)
	}
	b.log.Infof("yuna is online as %s (id %s); intents 0x%x",
		b.session.State.User.Username, b.appID, int(b.session.Identify.Intents))
	b.log.Infof("mention detection matches against id %s; any other id is a bug", b.appID)
	if count, err := b.store.CountAIChannels(); err != nil {
		b.log.Warnf("could not count ai channels: %v", err)
	} else {
		b.log.Infof("%d ai channel(s) registered; in a plain channel yuna only answers "+
			"mentions and replies", count)
	}

	<-ctx.Done()
	b.log.Infof("gateway closing")
	return nil
}

// gatewayHint turns Discord's terse gateway close codes into something
// actionable. A refused privileged intent is the failure everybody hits first.
func gatewayHint(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "4014") || strings.Contains(text, "Disallowed intent"):
		return " (Discord refused the intents this bot asked for: enable \"Message Content\" " +
			"and \"Server Members\" under Bot -> Privileged Gateway Intents in the " +
			"Discord Developer Portal, then start the bot again)"
	case strings.Contains(text, "4004") || strings.Contains(text, "Authentication failed"):
		return " (Discord rejected DISCORD_TOKEN)"
	}
	return ""
}

// submit queues a message for generation on the per-channel queue. One
// generation runs per channel at a time, and messages arriving during one
// collapse into a single follow-up that uses only the newest of them.
func (b *Bot) submit(msg *discordgo.Message) {
	b.queue.submit(msg)
}

// runExclusive runs fn while holding the channel's generation lock, so a slash
// command never interleaves with a reply already being written.
func (b *Bot) runExclusive(channelID string, fn func()) {
	b.queue.exclusive(channelID, fn)
}

// startTyping shows the typing indicator until the returned stop function is
// called.
func (b *Bot) startTyping(channelID string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.session.ChannelTyping(channelID)
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = b.session.ChannelTyping(channelID)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

// logAttribution records which provider and model actually answered a turn.
// Attribution is deliberately kept out of the Discord messages themselves, so
// the log is the only place it appears.
func (b *Bot) logAttribution(where string, resp *ai.Response) {
	b.log.Infof("%s answered by provider=%s model=%s fallback=%t",
		where, resp.Provider, resp.Model, resp.IsFallback)
}

// sendReply delivers an answer as one or more messages. The first chunk replies
// to the message that triggered it; the rest follow in the channel. It returns
// the first message sent so the reply can be stored.
func (b *Bot) sendReply(ref *discordgo.Message, text string) (*discordgo.Message, error) {
	chunks := Chunk(text)
	if len(chunks) == 0 {
		chunks = []string{emptyReply}
	}

	var first *discordgo.Message
	for i, chunk := range chunks {
		var (
			sent *discordgo.Message
			err  error
		)
		if i == 0 {
			sent, err = b.session.ChannelMessageSendReply(ref.ChannelID, chunk, ref.Reference())
		} else {
			sent, err = b.session.ChannelMessageSend(ref.ChannelID, chunk)
		}
		if err != nil {
			return first, fmt.Errorf("send chunk %d/%d: %w", i+1, len(chunks), err)
		}
		if i == 0 {
			first = sent
		}
	}
	return first, nil
}
