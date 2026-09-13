package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/memory"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// Slash command names, used both to build the command set and to dispatch it.
const (
	cmdChat                = "chat"
	cmdSetAIChannel        = "set_ai_channel"
	cmdRemoveAIChannel     = "remove_ai_channel"
	cmdListAIChannels      = "list_ai_channels"
	cmdProviderStatus      = "provider_status"
	cmdSetPreferredModel   = "set_preferred_model"
	cmdClearPreferredModel = "clear_preferred_model"
	cmdRemember            = "remember"
	cmdMemory              = "memory"
	cmdForget              = "forget"
)

// Commands is the complete slash command set, republished on every boot.
func Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        cmdChat,
			Description: "Ask Yuna something",
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "prompt",
				Description: "What you want to ask",
				Required:    true,
			}},
		},
		{
			Name:                     cmdSetAIChannel,
			Description:              "Make this channel one Yuna replies to every message in",
			DefaultMemberPermissions: adminOnly(),
		},
		{
			Name:                     cmdRemoveAIChannel,
			Description:              "Stop Yuna replying to every message in this channel",
			DefaultMemberPermissions: adminOnly(),
		},
		{
			Name:                     cmdListAIChannels,
			Description:              "List the channels Yuna replies to every message in",
			DefaultMemberPermissions: adminOnly(),
		},
		{
			Name:                     cmdProviderStatus,
			Description:              "Show the effective provider chain and settings",
			DefaultMemberPermissions: adminOnly(),
		},
		{
			Name:                     cmdSetPreferredModel,
			Description:              "Pin a provider to the front of the chain for this server",
			DefaultMemberPermissions: adminOnly(),
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "provider",
					Description: "Provider name, as configured in YUNA_PROVIDERS",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "model",
					Description: "Model ID to use instead of the configured one",
					Required:    false,
				},
			},
		},
		{
			Name:                     cmdClearPreferredModel,
			Description:              "Return this server to the configured provider order",
			DefaultMemberPermissions: adminOnly(),
		},
		{
			Name:        cmdRemember,
			Description: "Ask Yuna to remember something about you",
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "fact",
				Description: "What to remember",
				Required:    true,
			}},
		},
		{
			Name:        cmdMemory,
			Description: "Show what Yuna remembers about you here",
		},
		{
			Name:        cmdForget,
			Description: "Delete what Yuna remembers about you",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "all",
					Description: "Forget you in every server and DM, not just this one",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "include_history",
					Description: "Also delete your stored messages from this channel",
					Required:    false,
				},
			},
		},
	}
}

// adminOnly hides a command from anyone without the Administrator permission.
// isAdmin repeats the check at runtime, because per-guild command permission
// overrides can re-expose a command that Discord would otherwise hide.
func adminOnly() *int64 {
	permission := int64(discordgo.PermissionAdministrator)
	return &permission
}

// deferFor acknowledges the interaction. Discord requires a first response
// within three seconds, so every handler defers before doing any real work.
func (b *Bot) deferFor(e *discordgo.InteractionCreate, ephemeral bool) error {
	resp := &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{},
	}
	if ephemeral {
		resp.Data.Flags = discordgo.MessageFlagsEphemeral
	}
	return b.session.InteractionRespond(e.Interaction, resp)
}

// followup sends one message after a defer. The ephemeral flag is only honoured
// on the followup endpoint, which is exactly what this uses.
func (b *Bot) followup(e *discordgo.InteractionCreate, content string, ephemeral bool) error {
	params := &discordgo.WebhookParams{Content: content}
	if ephemeral {
		params.Flags = discordgo.MessageFlagsEphemeral
	}
	_, err := b.session.FollowupMessageCreate(e.Interaction, true, params)
	return err
}

// followupChunks delivers content too long for one message.
func (b *Bot) followupChunks(e *discordgo.InteractionCreate, text string, ephemeral bool) {
	chunks := Chunk(text)
	if len(chunks) == 0 {
		chunks = []string{emptyReply}
	}
	for _, chunk := range chunks {
		if err := b.followup(e, chunk, ephemeral); err != nil {
			b.log.Errorf("followup for /%s: %v", e.ApplicationCommandData().Name, err)
			return
		}
	}
}

// isAdmin reports whether the invoker holds the Administrator permission.
func isAdmin(e *discordgo.InteractionCreate) bool {
	if e.Member == nil {
		return false
	}
	return e.Member.Permissions&discordgo.PermissionAdministrator != 0
}

// requireAdmin refuses the command when the invoker is not an administrator.
func (b *Bot) requireAdmin(e *discordgo.InteractionCreate) bool {
	if isAdmin(e) {
		return true
	}
	_ = b.followup(e, "You need the Administrator permission to use this command.", true)
	return false
}

// optionValue returns the named option's raw value, or nil when it is absent.
func optionValue(data discordgo.ApplicationCommandInteractionData, name string) any {
	for _, opt := range data.Options {
		if opt != nil && opt.Name == name {
			return opt.Value
		}
	}
	return nil
}

// optionString reads a string option, returning "" when it is absent.
func optionString(data discordgo.ApplicationCommandInteractionData, name string) string {
	value, _ := optionValue(data, name).(string)
	return value
}

// optionBool reads a boolean option, returning false when it is absent.
func optionBool(data discordgo.ApplicationCommandInteractionData, name string) bool {
	value, _ := optionValue(data, name).(bool)
	return value
}

// turnFromInteraction describes the invoker for memory.
//
// Member is nil in a DM and User is nil in a guild, so neither can be assumed.
func turnFromInteraction(e *discordgo.InteractionCreate) memory.Turn {
	user := e.User
	member := e.Member
	if user == nil && member != nil {
		user = member.User
	}
	if user == nil {
		return memory.Turn{GuildID: e.GuildID, ChannelID: e.ChannelID}
	}
	return memory.Turn{
		GuildID:     e.GuildID,
		ChannelID:   e.ChannelID,
		UserID:      user.ID,
		Username:    user.Username,
		DisplayName: displayName(user, member),
		Nickname:    nickname(user, member),
	}
}

// interactionUserID returns the invoker's ID, checking both fields because only
// one of them is populated depending on whether the command ran in a guild.
func interactionUserID(e *discordgo.InteractionCreate) string {
	if e.Member != nil && e.Member.User != nil {
		return e.Member.User.ID
	}
	if e.User != nil {
		return e.User.ID
	}
	return ""
}

// cmdChat answers a one-off prompt.
//
// The prompt is not stored: /chat is an explicit question, not part of the
// channel's conversation history.
func (b *Bot) cmdChat(e *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, false); err != nil {
		b.log.Errorf("defer /%s: %v", cmdChat, err)
		return
	}

	prompt := strings.TrimSpace(optionString(data, "prompt"))
	if prompt == "" {
		_ = b.followup(e, "Please include a prompt.", false)
		return
	}

	turn := turnFromInteraction(e)
	chain := b.chain(turn.GuildID)

	b.runExclusive(e.ChannelID, func() {
		ctx, cancel := context.WithTimeout(context.Background(), generateTimeout)
		defer cancel()

		msgs, err := b.memory.Context(turn)
		if err != nil {
			b.log.Errorf("build context for /%s: %v", cmdChat, err)
			_ = b.followup(e, "I could not read my memory just now. Please try again.", false)
			return
		}
		// Rendered with the same author format as channel messages, so the model
		// still knows who is asking.
		msgs = append(msgs, schemas.ChatMessage{
			Role: schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(memory.FormatUserMessage(store.StoredMessage{
				Username:    turn.Username,
				DisplayName: turn.DisplayName,
				Nickname:    turn.Nickname,
				Role:        store.RoleUser,
				Content:     prompt,
			}))},
		})

		resp, err := b.chat.ChatWithTools(ctx, chain[0], chain, msgs, b.tools(),
			func(ctx context.Context, call ai.ToolCall) string { return b.runTool(turn, call) })
		if err != nil {
			b.log.Errorf("/%s failed: %v", cmdChat, err)
			_ = b.followup(e, "I could not reach any of my configured models just now.", false)
			return
		}

		text := strings.TrimSpace(resp.Text)
		if text == "" {
			text = emptyReply
		}
		for _, chunk := range Chunk(text) {
			if err := b.followup(e, chunk, false); err != nil {
				b.log.Errorf("/%s followup: %v", cmdChat, err)
				return
			}
		}
		b.logAttribution("/"+cmdChat, resp)
	})
}

// cmdSetAIChannel makes a channel one Yuna answers every message in.
func (b *Bot) cmdSetAIChannel(e *discordgo.InteractionCreate, _ discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdSetAIChannel, err)
		return
	}
	if !b.requireAdmin(e) {
		return
	}
	if e.GuildID == "" {
		_ = b.followup(e, "This command only works in a server.", true)
		return
	}
	if err := b.store.AddAIChannel(e.ChannelID, e.GuildID); err != nil {
		b.log.Errorf("add ai channel %s: %v", e.ChannelID, err)
		_ = b.followup(e, "I could not save that. Check the logs.", true)
		return
	}
	_ = b.followup(e, "This channel is now an AI channel. I will reply to every message here.", true)
}

// cmdRemoveAIChannel stops Yuna answering every message in a channel.
func (b *Bot) cmdRemoveAIChannel(e *discordgo.InteractionCreate, _ discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdRemoveAIChannel, err)
		return
	}
	if !b.requireAdmin(e) {
		return
	}
	if err := b.store.RemoveAIChannel(e.ChannelID); err != nil {
		b.log.Errorf("remove ai channel %s: %v", e.ChannelID, err)
		_ = b.followup(e, "I could not save that. Check the logs.", true)
		return
	}
	_ = b.followup(e, "This channel is no longer an AI channel.", true)
}

// cmdListAIChannels reports which channels in this server are AI channels.
func (b *Bot) cmdListAIChannels(e *discordgo.InteractionCreate, _ discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdListAIChannels, err)
		return
	}
	if !b.requireAdmin(e) {
		return
	}
	if e.GuildID == "" {
		_ = b.followup(e, "This command only works in a server.", true)
		return
	}

	channels, err := b.store.AIChannels(e.GuildID)
	if err != nil {
		b.log.Errorf("list ai channels for guild %s: %v", e.GuildID, err)
		_ = b.followup(e, "I could not read that. Check the logs.", true)
		return
	}
	if len(channels) == 0 {
		_ = b.followup(e, "No channels in this server are AI channels yet. Use /"+
			cmdSetAIChannel+" to add one.", true)
		return
	}

	var out strings.Builder
	fmt.Fprintf(&out, "**AI channels** (%d)\n", len(channels))
	for _, id := range channels {
		fmt.Fprintf(&out, "- <#%s>\n", id)
	}
	b.followupChunks(e, out.String(), true)
}

// cmdProviderStatus reports the chain that would actually be used right now.
func (b *Bot) cmdProviderStatus(e *discordgo.InteractionCreate, _ discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdProviderStatus, err)
		return
	}
	if !b.requireAdmin(e) {
		return
	}

	chain := b.chain(e.GuildID)
	var out strings.Builder
	out.WriteString("**Effective provider chain**\n")
	for i, p := range chain {
		role := "primary"
		if i > 0 {
			role = fmt.Sprintf("fallback %d", i)
		}
		fmt.Fprintf(&out, "%d. **%s** - `%s` (%s)\n", i+1, p.Name, p.Model, role)
		fmt.Fprintf(&out, "    key: %s; tools: %t", describeKey(p), p.Tools)
		if p.BaseURL != "" {
			fmt.Fprintf(&out, "; base URL: `%s`", p.BaseURL)
		}
		out.WriteString("\n")
	}

	if override, err := b.store.PreferredModel(e.GuildID); err == nil && override != nil {
		fmt.Fprintf(&out, "\n**Preferred model:** %s/`%s` (set by <@%s>)\n",
			override.ProviderName, override.ModelName, override.SetByUserID)
	} else {
		out.WriteString("\n**Preferred model:** none\n")
	}
	fmt.Fprintf(&out, "**Memory:** %t; history window: %d; summary every: %d messages",
		b.cfg.MemoryEnabled, b.cfg.HistoryWindow, b.cfg.SummaryEvery)

	b.followupChunks(e, out.String(), true)
}

func describeKey(p config.Provider) string {
	switch {
	case p.APIKey != "":
		return "set"
	case p.KeyLess:
		return "not needed"
	default:
		return "missing"
	}
}

// cmdSetPreferredModel pins one provider to the front of the chain for a guild.
func (b *Bot) cmdSetPreferredModel(e *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdSetPreferredModel, err)
		return
	}
	if !b.requireAdmin(e) {
		return
	}
	if e.GuildID == "" {
		_ = b.followup(e, "This command only works in a server.", true)
		return
	}

	name := strings.ToLower(strings.TrimSpace(optionString(data, "provider")))
	if name == "" {
		_ = b.followup(e, "Name a provider, for example: gemini", true)
		return
	}
	provider, ok := b.cfg.Provider(name)
	if !ok {
		_ = b.followup(e, fmt.Sprintf("Unknown provider %q. Configured providers: %s.",
			name, strings.Join(b.cfg.ProviderNames(), ", ")), true)
		return
	}

	model := strings.TrimSpace(optionString(data, "model"))
	if model == "" {
		model = provider.Model
	}
	if err := b.store.SetPreferredModel(e.GuildID, name, model, interactionUserID(e)); err != nil {
		b.log.Errorf("set preferred model for %s: %v", e.GuildID, err)
		_ = b.followup(e, "I could not save that. Check the logs.", true)
		return
	}
	_ = b.followup(e, fmt.Sprintf(
		"Pinned **%s** (`%s`) to the front of the chain for this server. The others remain as fallbacks. "+
			"Use /%s to undo.", name, model, cmdClearPreferredModel), true)
}

// cmdClearPreferredModel returns a guild to the configured provider order.
func (b *Bot) cmdClearPreferredModel(e *discordgo.InteractionCreate, _ discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdClearPreferredModel, err)
		return
	}
	if !b.requireAdmin(e) {
		return
	}
	if e.GuildID == "" {
		_ = b.followup(e, "This command only works in a server.", true)
		return
	}
	if err := b.store.ClearPreferredModel(e.GuildID); err != nil {
		b.log.Errorf("clear preferred model for %s: %v", e.GuildID, err)
		_ = b.followup(e, "I could not save that. Check the logs.", true)
		return
	}
	_ = b.followup(e, "Cleared. This server is back to the configured provider order.", true)
}

// cmdRemember stores one fact about the invoker, in the scope of this channel.
func (b *Bot) cmdRemember(e *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdRemember, err)
		return
	}
	content := strings.TrimSpace(optionString(data, "fact"))
	if content == "" {
		_ = b.followup(e, "Please include something to remember.", true)
		return
	}
	if err := b.memory.Remember(turnFromInteraction(e), content, store.SourceCommand); err != nil {
		b.log.Errorf("/%s: %v", cmdRemember, err)
		_ = b.followup(e, "I could not store that. Check the logs.", true)
		return
	}
	_ = b.followup(e, "Saved. I will remember that when we talk.", true)
}

// cmdMemory lists what Yuna remembers about the invoker in this scope. The
// reply is always ephemeral so one person's memories are never rendered to a
// channel of other people.
func (b *Bot) cmdMemory(e *discordgo.InteractionCreate, _ discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdMemory, err)
		return
	}
	turn := turnFromInteraction(e)
	facts, err := b.memory.FactsFor(turn, 0)
	if err != nil {
		b.log.Errorf("/%s: %v", cmdMemory, err)
		_ = b.followup(e, "I could not read my memory just now. Check the logs.", true)
		return
	}
	if len(facts) == 0 {
		_ = b.followup(e, "I do not remember anything about you here yet.", true)
		return
	}

	where := "in this server"
	if turn.GuildID == "" {
		where = "in this DM"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "**What I remember about you %s** (%d)\n", where, len(facts))
	for _, f := range facts {
		fmt.Fprintf(&out, "- %s\n", f.Content)
	}
	b.followupChunks(e, out.String(), true)
}

// cmdForget deletes the invoker's facts, and optionally their stored messages
// from this channel.
func (b *Bot) cmdForget(e *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdForget, err)
		return
	}
	all := optionBool(data, "all")
	includeHistory := optionBool(data, "include_history")

	facts, messages, err := b.memory.Forget(turnFromInteraction(e), all, includeHistory)
	if err != nil {
		b.log.Errorf("/%s: %v", cmdForget, err)
		_ = b.followup(e, "I could not delete that. Check the logs.", true)
		return
	}

	where := "in this server"
	if all {
		where = "everywhere"
	}
	reply := fmt.Sprintf("Deleted %d fact(s) %s.", facts, where)
	if includeHistory {
		reply += fmt.Sprintf(" Also removed %d of your stored message(s) from this channel.", messages)
	}
	_ = b.followup(e, reply, true)
}
