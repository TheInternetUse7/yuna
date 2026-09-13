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
	rememberToolName  = "remember"
	emptyReply        = "I could not come up with anything to say to that."
	noImageModelReply = "None of my configured models can see images right now."
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
		b.log.Debugf("message %s in channel %s from %s: %s (ai_channel=%t mentions=%d images=%d)",
			msg.ID, msg.ChannelID, msg.Author.ID, verdict.reason, isAIChannel,
			len(msg.Mentions), len(imageAttachments(msg)))
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
	chain := b.chain(msg.GuildID, msg.Author.ID)
	images := imageAttachments(msg)
	if len(images) > 0 {
		chain = imageChain(chain)
		b.log.Debugf("message %s carries %d image(s); provider chain narrowed to %v",
			msg.ID, len(images), providerNames(chain))
		if len(chain) == 0 {
			b.log.Warnf("message %s in channel %s has images but no configured model supports image input",
				msg.ID, msg.ChannelID)
			if _, err := b.sendReply(msg, noImageModelReply); err != nil {
				b.log.Errorf("send no-image-model notice in channel %s: %v", msg.ChannelID, err)
			}
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), generateTimeout)
	defer cancel()

	msgs, err := b.memory.Context(turn)
	if err != nil {
		b.log.Errorf("build context for channel %s: %v", msg.ChannelID, err)
		return
	}

	stopTyping := b.startTyping(msg.ChannelID)
	defer stopTyping()

	msgs = attachImages(msgs, images)

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

// chain returns the configured providers in order, with a stored model
// preference applied: the chosen provider moves to the front and answers with
// the chosen model, which becomes Providers[0] for the turn. Everything else
// stays in configured order as fallbacks.
//
// A guild preference wins when there is one; otherwise a DM preference keyed by
// the user applies. The user argument is ignored inside a guild, so the same
// call shape works from both paths.
func (b *Bot) chain(guildID, userID string) []config.Provider {
	configured := b.cfg.Providers

	pref := b.resolvePreference(guildID, userID, false)
	if pref == nil {
		return configured
	}

	index := -1
	for i, p := range configured {
		if p.Name == pref.ProviderName {
			index = i
			break
		}
	}
	if index < 0 {
		b.log.Warnf("model preference names provider %q, which is no longer configured; ignoring it",
			pref.ProviderName)
		return configured
	}

	chosen := configured[index]
	if !chosen.HasModel(pref.ModelName) {
		// The operator renamed or dropped the model. Falling back to the chain
		// is quieter than failing every turn from a stale preference.
		b.log.Warnf("model preference names model %q, which provider %q no longer lists; ignoring it",
			pref.ModelName, pref.ProviderName)
		return configured
	}
	chosen.Models = reorderModel(chosen.Models, pref.ModelName)

	if index == 0 {
		// Already the head of the chain: only the model order can change.
		reordered := make([]config.Provider, len(configured))
		copy(reordered, configured)
		reordered[0] = chosen
		return reordered
	}

	reordered := make([]config.Provider, 0, len(configured))
	reordered = append(reordered, chosen)
	for i, p := range configured {
		if i != index {
			reordered = append(reordered, p)
		}
	}
	return reordered
}

// reorderModel moves model to the front of a provider's model list, keeping the
// rest in their configured order.
func reorderModel(models []string, model string) []string {
	out := make([]string, 0, len(models))
	out = append(out, model)
	for _, m := range models {
		if m != model {
			out = append(out, m)
		}
	}
	return out
}

// providerNames lists a chain's provider names in order, for logs that explain
// which ladder a turn actually uses.
func providerNames(chain []config.Provider) []string {
	names := make([]string, 0, len(chain))
	for _, p := range chain {
		names = append(names, p.Name)
	}
	return names
}

// preferenceScope resolves which stored preference applies to an interaction:
// the guild for an administrator acting in a server, otherwise the user's own
// DM scope. The boolean is false when the invoker may not change the guild's
// model, which is the only refusal case.
func (b *Bot) preferenceScope(e *discordgo.InteractionCreate) (string, string, bool) {
	if e.GuildID != "" {
		if !isAdmin(e) {
			return "", "", false
		}
		return store.PreferenceScopeGuild, e.GuildID, true
	}
	userID := interactionUserID(e)
	if userID == "" {
		return "", "", false
	}
	return store.PreferenceScopeDM, userID, true
}

// resolvePreference reads the stored model choice that applies to a turn:
// the guild's, or the speaker's DM choice. A lookup failure is reported and
// treated as "no preference" so a database hiccup cannot block a reply.
func (b *Bot) resolvePreference(guildID, userID string, warn bool) *store.ModelPreference {
	var scopeType, scopeID string
	if guildID != "" {
		scopeType, scopeID = store.PreferenceScopeGuild, guildID
	} else if userID != "" {
		scopeType, scopeID = store.PreferenceScopeDM, userID
	} else {
		return nil
	}

	pref, err := b.store.ModelPreference(scopeType, scopeID)
	if err != nil {
		if warn {
			b.log.Warnf("model preference lookup for %s %s: %v", scopeType, scopeID, err)
		}
		return nil
	}
	return pref
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
	switch e.Type {
	case discordgo.InteractionApplicationCommand:
		go b.handleInteraction(e)
	case discordgo.InteractionApplicationCommandAutocomplete:
		go b.handleAutocomplete(e)
	default:
		return
	}
}

// handleAutocomplete answers the typeahead for a command option. It runs
// without a prior defer: Discord expects the choices as the interaction's
// first and only response.
func (b *Bot) handleAutocomplete(e *discordgo.InteractionCreate) {
	data := e.ApplicationCommandData()
	if data.Name != cmdModel {
		return
	}
	b.cmdModelAutocomplete(e, data)
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
	case cmdModel:
		return b.cmdModel
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
