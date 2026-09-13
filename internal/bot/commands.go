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
	cmdChat            = "chat"
	cmdSetAIChannel    = "set_ai_channel"
	cmdRemoveAIChannel = "remove_ai_channel"
	cmdListAIChannels  = "list_ai_channels"
	cmdProviderStatus  = "provider_status"
	cmdModel           = "model"
	cmdModelSet        = "set"
	cmdModelReset      = "reset"
	cmdRemember        = "remember"
	cmdMemory          = "memory"
	cmdForget          = "forget"
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
			// No admin gate: in a DM the choice is personal, and in a guild the
			// permission check runs inside the handler so the two cases can
			// differ. Discord only lets one permission set be published.
			Name:        cmdModel,
			Description: "Choose which model answers here",
			Options: []*discordgo.ApplicationCommandOption{
				{
					// Options are either all subcommands or all plain options;
					// mixing the two is rejected with code 50035.
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        cmdModelSet,
					Description: "Set the model for this server or your DMs",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:         discordgo.ApplicationCommandOptionString,
							Name:         "id",
							Description:  "Model to use; type to search the configured models",
							Required:     true,
							Autocomplete: true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        cmdModelReset,
					Description: "Go back to the configured model order",
				},
			},
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
//
// It takes an option slice rather than the whole interaction so it works at
// either level: a command's own options, or the options belonging to a
// subcommand. Callers with a subcommand pass subcommandOptions(data).
func optionValue(options []*discordgo.ApplicationCommandInteractionDataOption, name string) any {
	for _, opt := range options {
		if opt != nil && opt.Name == name {
			return opt.Value
		}
	}
	return nil
}

// optionString reads a string option, returning "" when it is absent.
func optionString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	value, _ := optionValue(options, name).(string)
	return value
}

// optionBool reads a boolean option, returning false when it is absent.
func optionBool(options []*discordgo.ApplicationCommandInteractionDataOption, name string) bool {
	value, _ := optionValue(options, name).(bool)
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

	prompt := strings.TrimSpace(optionString(data.Options, "prompt"))
	if prompt == "" {
		_ = b.followup(e, "Please include a prompt.", false)
		return
	}

	turn := turnFromInteraction(e)
	chain := b.chain(turn.GuildID, turn.UserID)

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

	userID := interactionUserID(e)
	chain := b.chain(e.GuildID, userID)
	var out strings.Builder
	out.WriteString("**Effective provider chain**\n")
	for i, p := range chain {
		role := "primary"
		if i > 0 {
			role = fmt.Sprintf("fallback %d", i)
		}
		fmt.Fprintf(&out, "%d. **%s** (%s)\n", i+1, p.Name, role)
		for j, m := range p.Models {
			marker := "  "
			if j == 0 {
				marker = "> "
			}
			fmt.Fprintf(&out, "    %s`%s`\n", marker, m)
		}
		fmt.Fprintf(&out, "    key: %s; tools: %t", describeKey(p), p.Tools)
		if p.BaseURL != "" {
			fmt.Fprintf(&out, "; base URL: `%s`", p.BaseURL)
		}
		out.WriteString("\n")
	}

	if pref := b.resolvePreference(e.GuildID, userID, true); pref != nil {
		fmt.Fprintf(&out, "\n**Selected model:** %s/`%s` (%s, set by <@%s>)\n",
			pref.ProviderName, pref.ModelName, describePreferenceScope(pref.ScopeType), pref.SetByUserID)
	} else {
		out.WriteString("\n**Selected model:** none, using the configured order\n")
	}
	fmt.Fprintf(&out, "**Memory:** %t; history window: %d; summary every: %d messages",
		b.cfg.MemoryEnabled, b.cfg.HistoryWindow, b.cfg.SummaryEvery)
	if b.cfg.SummaryModel != "" {
		fmt.Fprintf(&out, "; summarised by `%s` on %s",
			b.cfg.SummaryModel, b.cfg.SummaryProvider)
	}

	b.followupChunks(e, out.String(), true)
}

// describePreferenceScope renders a stored preference's scope for humans.
func describePreferenceScope(scopeType string) string {
	if scopeType == store.PreferenceScopeDM {
		return "this user's DMs"
	}
	return "this server"
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

// cmdModel chooses which configured model answers in this context: for a whole
// server when the invoker is an administrator, and for the caller's own DMs
// otherwise. It has no plain options -- Discord forbids mixing subcommands with
// other option types -- so every invocation arrives as set or reset.
func (b *Bot) cmdModel(e *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdModel, err)
		return
	}

	subcommand := optionSubcommand(data)
	if subcommand != cmdModelSet && subcommand != cmdModelReset {
		_ = b.followup(e, fmt.Sprintf("Use `/%s %s id:<model>` to choose a model, or `/%s %s` to clear it.",
			cmdModel, cmdModelSet, cmdModel, cmdModelReset), true)
		return
	}

	scopeType, scopeID, ok := b.preferenceScope(e)
	if !ok {
		_ = b.followup(e, "You need the Administrator permission to change the model for a server. "+"In a direct message you can set your own model.", true)
		return
	}

	if subcommand == cmdModelReset {
		if err := b.store.ClearModelPreference(scopeType, scopeID); err != nil {
			b.log.Errorf("clear model preference for %s %s: %v", scopeType, scopeID, err)
			_ = b.followup(e, "I could not save that. Check the logs.", true)
			return
		}
		_ = b.followup(e, "Cleared. Back to the configured model order.", true)
		return
	}

	// set's options live inside the subcommand, one level below the top.
	options := subcommandOptions(data)
	id := strings.TrimSpace(optionString(options, "id"))
	if id == "" {
		_ = b.followup(e, fmt.Sprintf(
			"Name a model. Type in the `id` option to search, or use `/%s %s` to clear the choice.",
			cmdModel, cmdModelReset), true)
		return
	}

	chain := b.chain(e.GuildID, interactionUserID(e))
	provider, found, ambiguous := findModel(chain, id)
	if !found {
		_ = b.followup(e, fmt.Sprintf("Unknown model %q. It is not in any configured provider's model list.", id), true)
		return
	}
	if ambiguous {
		// The same model ID under two providers has different credentials and
		// different fallback ladders behind it, so say which one was taken
		// rather than picking silently.
		b.log.Warnf("model %q is configured by more than one provider; using %q", id, provider.Name)
	}

	if err := b.store.SetModelPreference(store.ModelPreference{
		ScopeType:    scopeType,
		ScopeID:      scopeID,
		ProviderName: provider.Name,
		ModelName:    id,
		SetByUserID:  interactionUserID(e),
	}); err != nil {
		b.log.Errorf("set model preference for %s %s: %v", scopeType, scopeID, err)
		_ = b.followup(e, "I could not save that. Check the logs.", true)
		return
	}

	where := "for your direct messages"
	if scopeType == store.PreferenceScopeGuild {
		where = "for this server"
	}
	_ = b.followup(e, fmt.Sprintf(
		"Using **%s** (`%s`) %s. That provider's other models answer first if this one fails, "+
			"then the rest of the chain. `/%s %s` undoes it.",
		provider.Name, id, where, cmdModel, cmdModelReset), true)
}

// cmdModelAutocomplete answers the /model typeahead. Candidacy comes from the
// configured model lists only, so the response never depends on a provider
// being reachable inside Discord's three-second window.
func (b *Bot) cmdModelAutocomplete(e *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	typed := ""
	if opt := focusedOption(data); opt != nil {
		typed = opt.StringValue()
	}

	candidates := autocompleteCandidates(b.cfg.Providers)
	choices := matchChoices(candidates, typed)
	if len(choices) == 0 {
		choices = []*discordgo.ApplicationCommandOptionChoice{
			{Name: "no configured model matches", Value: typed},
		}
	}

	err := b.session.InteractionRespond(e.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
	if err != nil {
		b.log.Errorf("/%s autocomplete: %v", cmdModel, err)
	}
}

// autocompleteMax is Discord's cap on choices returned for one autocomplete
// interaction; sending more is rejected outright.
const autocompleteMax = 25

// matchChoices filters candidate models by what has been typed so far, matching
// case-insensitively on the model ID or the provider name, and caps the result
// at Discord's limit. The choice value is the raw model ID, not the label:
// Discord echoes the value back, and the handler resolves it with findModel.
func matchChoices(candidates []modelCandidate, typed string) []*discordgo.ApplicationCommandOptionChoice {
	needle := strings.ToLower(strings.TrimSpace(typed))
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, autocompleteMax)
	for _, c := range candidates {
		if needle != "" && !strings.Contains(strings.ToLower(c.label()), needle) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: c.label(), Value: c.Model})
		if len(choices) == autocompleteMax {
			break
		}
	}
	return choices
}

// modelCandidate is one picker entry. The label names the provider because a
// raw model ID does not say which credentials and fallback ladder sit behind
// it; the value stays the raw ID so a picked entry and a hand-typed one resolve
// the same way.
type modelCandidate struct {
	Provider string
	Model    string
}

func (c modelCandidate) label() string { return c.Provider + " · " + c.Model }

// autocompleteCandidates lists every configured model, in chain order.
func autocompleteCandidates(providers []config.Provider) []modelCandidate {
	out := make([]modelCandidate, 0, len(providers))
	for _, p := range providers {
		for _, m := range p.Models {
			out = append(out, modelCandidate{Provider: p.Name, Model: m})
		}
	}
	return out
}

// findModel resolves a model ID against the chain, preferring the provider that
// would answer right now so a bare ID keeps naming the same vendor as long as
// that provider still lists it. ambiguous reports that another provider also
// offers the ID.
func findModel(chain []config.Provider, id string) (config.Provider, bool, bool) {
	var found config.Provider
	var ok bool
	ambiguous := false
	for _, p := range chain {
		if !p.HasModel(id) {
			continue
		}
		if !ok {
			found, ok = p, true
			continue
		}
		ambiguous = true
	}
	return found, ok, ambiguous
}

// optionSubcommand returns the name of the invoked subcommand, or "" when the
// interaction used a top-level option instead.
func optionSubcommand(data discordgo.ApplicationCommandInteractionData) string {
	for _, opt := range data.Options {
		if opt != nil && opt.Type == discordgo.ApplicationCommandOptionSubCommand {
			return opt.Name
		}
	}
	return ""
}

// subcommandOptions returns the options belonging to the invoked subcommand,
// one level below the command's own options. It returns nil when the
// interaction carried plain options instead.
func subcommandOptions(data discordgo.ApplicationCommandInteractionData) []*discordgo.ApplicationCommandInteractionDataOption {
	for _, opt := range data.Options {
		if opt != nil && opt.Type == discordgo.ApplicationCommandOptionSubCommand {
			return opt.Options
		}
	}
	return nil
}

// focusedOption returns the option Discord marked as focused in an
// autocomplete interaction, or nil when none is flagged. The flag sits on the
// option being typed in, which for a subcommand is nested one level below the
// top, so the search recurses.
func focusedOption(data discordgo.ApplicationCommandInteractionData) *discordgo.ApplicationCommandInteractionDataOption {
	for _, opt := range data.Options {
		if found := focusedIn(opt); found != nil {
			return found
		}
	}
	return nil
}

func focusedIn(opt *discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	if opt == nil {
		return nil
	}
	if opt.Focused {
		return opt
	}
	for _, child := range opt.Options {
		if found := focusedIn(child); found != nil {
			return found
		}
	}
	return nil
}

// cmdRemember stores one fact about the invoker, in the scope of this channel.
func (b *Bot) cmdRemember(e *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if err := b.deferFor(e, true); err != nil {
		b.log.Errorf("defer /%s: %v", cmdRemember, err)
		return
	}
	content := strings.TrimSpace(optionString(data.Options, "fact"))
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
	all := optionBool(data.Options, "all")
	includeHistory := optionBool(data.Options, "include_history")

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
