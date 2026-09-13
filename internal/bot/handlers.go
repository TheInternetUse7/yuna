package bot

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/memory"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// generateTimeout bounds one answer, tool round trip included.
const generateTimeout = 2 * time.Minute

const (
	rememberToolName = "remember"
	emptyReply       = "I could not come up with anything to say to that."
)

// onMessage is the gateway entry point for messages.
func (b *Bot) onMessage(_ *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Message == nil || e.Message.Author == nil || e.Message.Author.Bot {
		return
	}
	// Handlers run on the gateway goroutine, so a model call here would stall
	// heartbeats and every other event. All work happens off it.
	go b.handleMessage(e.Message)
}

func (b *Bot) handleMessage(msg *discordgo.Message) {
	isAIChannel, err := b.store.IsAIChannel(msg.ChannelID)
	if err != nil {
		b.log.Errorf("check ai channel %s: %v", msg.ChannelID, err)
		return
	}

	replyToBot := false
	if referencedID(msg) != "" {
		replyToBot = b.replyChainReachesBot(msg.ChannelID, msg)
	}

	verdict := classify(msg, b.appID, isAIChannel, replyToBot)
	// Messages Yuna has no reason to act on are dropped without a line. Logging
	// every message in every visible channel buries the triggers the bot answers
	// under noise, so only a message she stores or answers is worth reporting.
	if !verdict.store && !verdict.respond {
		return
	}
	if verdict.respond {
		b.log.Debugf("message %s in channel %s from %s: %s (ai_channel=%t mentions=%d)",
			msg.ID, msg.ChannelID, msg.Author.ID, verdict.reason, isAIChannel, len(msg.Mentions))
	}
	if verdict.store {
		if err := b.storeMessage(msg, msg.Author.ID, store.RoleUser, msg.Content); err != nil {
			b.log.Errorf("store message %s: %v", msg.ID, err)
		}
	}
	if !verdict.respond {
		return
	}
	b.submit(msg)
}

// storeMessage persists one message and asks memory whether this channel now
// has enough new material for a summary refresh.
func (b *Bot) storeMessage(msg *discordgo.Message, userID, role, content string) error {
	_, err := b.store.InsertMessage(store.StoredMessage{
		MessageID:        msg.ID,
		GuildID:          msg.GuildID,
		ChannelID:        msg.ChannelID,
		UserID:           userID,
		Username:         msg.Author.Username,
		DisplayName:      displayName(msg.Author, msg.Member),
		Nickname:         nickname(msg.Author, msg.Member),
		Role:             role,
		Content:          content,
		ReplyToMessageID: referencedID(msg),
	})
	if err != nil {
		return err
	}
	b.memory.MaybeSummarize(msg.ChannelID, msg.GuildID)
	return nil
}

// generate answers one message and stores the reply.
func (b *Bot) generate(msg *discordgo.Message) {
	b.log.Debugf("generating reply for message %s in channel %s", msg.ID, msg.ChannelID)
	turn := turnFromMessage(msg)
	chain := b.chain(msg.GuildID)

	ctx, cancel := context.WithTimeout(context.Background(), generateTimeout)
	defer cancel()

	msgs, err := b.memory.Context(turn)
	if err != nil {
		b.log.Errorf("build context for channel %s: %v", msg.ChannelID, err)
		return
	}

	stopTyping := b.startTyping(msg.ChannelID)
	defer stopTyping()

	resp, err := b.chat.ChatWithTools(ctx, chain[0], chain, msgs, b.tools(),
		func(ctx context.Context, call ai.ToolCall) string {
			return b.runTool(turn, call)
		})
	if err != nil {
		b.log.Errorf("generate reply in channel %s: %v", msg.ChannelID, err)
		stopTyping()
		b.sendFailure(msg)
		return
	}
	stopTyping()

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		text = emptyReply
	}
	sent, err := b.sendReply(msg, text)
	if err != nil {
		b.log.Errorf("send reply in channel %s: %v", msg.ChannelID, err)
		return
	}
	b.logAttribution("reply in channel "+msg.ChannelID, resp)
	if sent == nil {
		return
	}
	if err := b.storeMessage(sent, b.appID, store.RoleAssistant, text); err != nil {
		b.log.Errorf("store reply %s: %v", sent.ID, err)
	}
}

// tools lists the tools offered to the model. A nil slice means the request
// carries no tool schema at all.
func (b *Bot) tools() []schemas.ChatTool {
	if !b.cfg.MemoryEnabled {
		return nil
	}
	return []schemas.ChatTool{memory.RememberTool()}
}

// runTool executes a tool call locally. Only remember is accepted, and the
// scope it writes to comes from the turn, never from the model.
func (b *Bot) runTool(turn memory.Turn, call ai.ToolCall) string {
	if call.Name != rememberToolName {
		return toolError("unknown tool " + call.Name)
	}
	content, err := memory.ParseRememberArgs(call.Arguments)
	if err != nil {
		return toolError(err.Error())
	}
	if err := b.memory.Remember(turn, content, store.SourceTool); err != nil {
		return toolError(err.Error())
	}
	b.log.Debugf("remembered a fact for %s", turn.UserID)
	return `{"ok":true}`
}

func toolError(message string) string {
	payload, err := json.Marshal(map[string]any{"ok": false, "error": message})
	if err != nil {
		return `{"ok":false}`
	}
	return string(payload)
}

// chain returns the configured providers in order, except that a guild's
// preferred-model override moves that provider to the front.
func (b *Bot) chain(guildID string) []config.Provider {
	configured := b.cfg.Providers
	if guildID == "" {
		return configured
	}

	override, err := b.store.PreferredModel(guildID)
	if err != nil {
		b.log.Warnf("preferred model lookup for guild %s: %v", guildID, err)
		return configured
	}
	if override == nil {
		return configured
	}

	index := -1
	for i, p := range configured {
		if p.Name == override.ProviderName {
			index = i
			break
		}
	}
	if index < 0 {
		b.log.Warnf("guild %s prefers provider %q, which is no longer configured; ignoring the override",
			guildID, override.ProviderName)
		return configured
	}
	if index == 0 && override.ModelName == configured[0].Model {
		return configured
	}

	preferred := configured[index]
	if override.ModelName != "" {
		preferred.Model = override.ModelName
	}
	reordered := make([]config.Provider, 0, len(configured))
	reordered = append(reordered, preferred)
	for i, p := range configured {
		if i != index {
			reordered = append(reordered, p)
		}
	}
	return reordered
}

func (b *Bot) sendFailure(ref *discordgo.Message) {
	const text = "I could not reach any of my configured models just now. Please try again in a moment."
	if _, err := b.session.ChannelMessageSendReply(ref.ChannelID, text, ref.Reference()); err != nil {
		b.log.Errorf("send failure notice: %v", err)
	}
}

// turnFromMessage describes the speaker for memory.
func turnFromMessage(msg *discordgo.Message) memory.Turn {
	return memory.Turn{
		GuildID:     msg.GuildID,
		ChannelID:   msg.ChannelID,
		UserID:      msg.Author.ID,
		Username:    msg.Author.Username,
		DisplayName: displayName(msg.Author, msg.Member),
		Nickname:    nickname(msg.Author, msg.Member),
	}
}

// displayName mirrors the original bot's "display_name": the global name when
// set, otherwise the account name.
func displayName(user *discordgo.User, _ *discordgo.Member) string {
	if user == nil {
		return ""
	}
	if user.GlobalName != "" {
		return user.GlobalName
	}
	return user.Username
}

// nickname mirrors the original bot's "server_nickname": the per-server name,
// falling back to the display name.
func nickname(user *discordgo.User, member *discordgo.Member) string {
	if member != nil && member.Nick != "" {
		return member.Nick
	}
	return displayName(user, member)
}

// onInteraction is the gateway entry point for slash commands.
func (b *Bot) onInteraction(_ *discordgo.Session, e *discordgo.InteractionCreate) {
	if e == nil || e.Interaction == nil {
		return
	}
	if e.Type != discordgo.InteractionApplicationCommand {
		return
	}
	go b.handleInteraction(e)
}

// interactionHandler is the signature every slash command shares. Returning it
// from a lookup rather than inlining a switch lets a test prove that every
// command Commands() publishes is actually dispatched.
func (b *Bot) handlerFor(name string) func(*discordgo.InteractionCreate, discordgo.ApplicationCommandInteractionData) {
	switch name {
	case cmdChat:
		return b.cmdChat
	case cmdSetAIChannel:
		return b.cmdSetAIChannel
	case cmdRemoveAIChannel:
		return b.cmdRemoveAIChannel
	case cmdListAIChannels:
		return b.cmdListAIChannels
	case cmdProviderStatus:
		return b.cmdProviderStatus
	case cmdSetPreferredModel:
		return b.cmdSetPreferredModel
	case cmdClearPreferredModel:
		return b.cmdClearPreferredModel
	case cmdRemember:
		return b.cmdRemember
	case cmdMemory:
		return b.cmdMemory
	case cmdForget:
		return b.cmdForget
	}
	return nil
}

func (b *Bot) handleInteraction(e *discordgo.InteractionCreate) {
	data := e.ApplicationCommandData()
	handler := b.handlerFor(data.Name)
	if handler == nil {
		b.log.Warnf("ignoring unknown command %q", data.Name)
		return
	}
	handler(e, data)
}
