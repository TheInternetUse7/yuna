package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestIsAdmin(t *testing.T) {
	cases := []struct {
		name   string
		perms  int64
		member *discordgo.Member
		want   bool
	}{
		{"administrator", discordgo.PermissionAdministrator, &discordgo.Member{}, true},
		{"administrator alongside others", discordgo.PermissionAdministrator | 16, &discordgo.Member{}, true},
		{"other permissions only", 16, &discordgo.Member{}, false},
		{"no permissions", 0, &discordgo.Member{}, false},
		{"direct message has no member", 0, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.member != nil {
				tc.member.Permissions = tc.perms
			}
			event := &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{Member: tc.member},
			}
			if got := isAdmin(event); got != tc.want {
				t.Fatalf("isAdmin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOptionParsing(t *testing.T) {
	data := discordgo.ApplicationCommandInteractionData{
		Name: cmdForget,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "all", Type: discordgo.ApplicationCommandOptionBoolean, Value: true},
			{Name: "include_history", Type: discordgo.ApplicationCommandOptionBoolean, Value: false},
			{Name: "provider", Type: discordgo.ApplicationCommandOptionString, Value: "gemini"},
			{Name: "unset", Type: discordgo.ApplicationCommandOptionString, Value: nil},
			{Name: "wrong_type", Type: discordgo.ApplicationCommandOptionString, Value: 42},
		},
	}

	if got := optionString(data.Options, "provider"); got != "gemini" {
		t.Fatalf("optionString(provider) = %q, want gemini", got)
	}
	if got := optionString(data.Options, "absent"); got != "" {
		t.Fatalf("optionString(absent) = %q, want empty", got)
	}
	if got := optionString(data.Options, "unset"); got != "" {
		t.Fatalf("optionString(unset) = %q, want empty", got)
	}
	if got := optionString(data.Options, "wrong_type"); got != "" {
		t.Fatalf("optionString(wrong type) = %q, want empty rather than a panic", got)
	}

	if !optionBool(data.Options, "all") {
		t.Fatal("optionBool(all) = false, want true")
	}
	if optionBool(data.Options, "include_history") {
		t.Fatal("optionBool(include_history) = true, want false")
	}
	if optionBool(data.Options, "absent") {
		t.Fatal("optionBool(absent) = true, want false")
	}
	if optionBool(data.Options, "provider") {
		t.Fatal("optionBool on a string option = true, want false")
	}
}

func TestOptionParsingToleratesNilOption(t *testing.T) {
	data := discordgo.ApplicationCommandInteractionData{
		Options: []*discordgo.ApplicationCommandInteractionDataOption{nil},
	}
	if got := optionString(data.Options, "provider"); got != "" {
		t.Fatalf("optionString with a nil option = %q, want empty", got)
	}
	if optionBool(data.Options, "all") {
		t.Fatal("optionBool with a nil option = true, want false")
	}
}

func TestSubcommandOptionsResolveNestedValues(t *testing.T) {
	data := discordgo.ApplicationCommandInteractionData{
		Name: cmdModel,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{
				Name: cmdModelSet,
				Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "id", Type: discordgo.ApplicationCommandOptionString, Value: "gemini/gemini-2.5-flash"},
				},
			},
		},
	}
	if got := optionSubcommand(data); got != cmdModelSet {
		t.Fatalf("optionSubcommand = %q, want %q", got, cmdModelSet)
	}
	if got := optionString(subcommandOptions(data), "id"); got != "gemini/gemini-2.5-flash" {
		t.Fatalf("nested id = %q, want gemini/gemini-2.5-flash", got)
	}

	// A command with plain options keeps them at the top level and must still
	// resolve, so the helper split did not break the flat case.
	plain := discordgo.ApplicationCommandInteractionData{
		Name: cmdForget,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "all", Type: discordgo.ApplicationCommandOptionBoolean, Value: true},
		},
	}
	if got := optionSubcommand(plain); got != "" {
		t.Fatalf("optionSubcommand on a plain command = %q, want empty", got)
	}
	if subcommandOptions(plain) != nil {
		t.Fatal("subcommandOptions on a plain command = non-nil, want nil")
	}
	if !optionBool(plain.Options, "all") {
		t.Fatal("plain all = false, want true")
	}
}

func TestFocusedOptionFindsNestedOption(t *testing.T) {
	// Discord flags the option being typed in, which for a subcommand sits one
	// level below the top; a flat scan of data.Options would miss it.
	data := discordgo.ApplicationCommandInteractionData{
		Name: cmdModel,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{
				Name: cmdModelSet,
				Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "id", Type: discordgo.ApplicationCommandOptionString, Value: "gem", Focused: true},
				},
			},
		},
	}
	got := focusedOption(data)
	if got == nil {
		t.Fatal("focusedOption found nothing in a subcommand interaction")
	}
	if got.Name != "id" {
		t.Fatalf("focusedOption = %q, want id", got.Name)
	}
	if got.StringValue() != "gem" {
		t.Fatalf("focused value = %q, want gem", got.StringValue())
	}

	if focusedOption(discordgo.ApplicationCommandInteractionData{}) != nil {
		t.Fatal("focusedOption on an empty interaction = non-nil, want nil")
	}
	nilChild := discordgo.ApplicationCommandInteractionData{
		Options: []*discordgo.ApplicationCommandInteractionDataOption{nil},
	}
	if focusedOption(nilChild) != nil {
		t.Fatal("focusedOption with a nil option = non-nil, want nil")
	}
}

func TestCommandsAreWellFormed(t *testing.T) {
	commands := Commands()
	if len(commands) != 9 {
		t.Fatalf("got %d commands, want 9", len(commands))
	}

	seen := make(map[string]bool)
	for _, c := range commands {
		if c == nil {
			t.Fatal("nil command in the set")
		}
		if c.Name == "" || c.Description == "" {
			t.Fatalf("command %+v is missing a name or description", c)
		}
		if seen[c.Name] {
			t.Fatalf("duplicate command name %q", c.Name)
		}
		seen[c.Name] = true
		if len(c.Name) > 32 {
			t.Fatalf("command name %q exceeds Discord's 32 character limit", c.Name)
		}
		if len(c.Description) > 100 {
			t.Fatalf("description for %q exceeds Discord's 100 character limit", c.Name)
		}
		validateOptions(t, c.Name, c.Options, true)
	}

	for _, name := range []string{
		cmdChat, cmdSetAIChannel, cmdRemoveAIChannel, cmdListAIChannels,
		cmdProviderStatus, cmdModel, cmdRemember, cmdMemory, cmdForget,
	} {
		if !seen[name] {
			t.Fatalf("command %q is not published", name)
		}
	}
}

// validateOptions enforces the structural rules Discord applies when commands
// are registered. The one that is easy to trip by accident: a single option
// list is either all subcommands or all plain options, never both. Violating it
// fails the whole registration call with code 50035, so nothing gets published.
func validateOptions(t *testing.T, command string, options []*discordgo.ApplicationCommandOption, topLevel bool) {
	t.Helper()
	if len(options) > 25 {
		t.Fatalf("command %q has %d options at one level; Discord allows at most 25", command, len(options))
	}

	subcommands := 0
	for _, opt := range options {
		if opt == nil {
			t.Fatalf("command %q has a nil option", command)
		}
		if opt.Type == discordgo.ApplicationCommandOptionSubCommand {
			subcommands++
		}
	}
	if subcommands > 0 {
		if !topLevel {
			t.Fatalf("command %q nests subcommands below the top level", command)
		}
		if subcommands != len(options) {
			t.Fatalf("command %q mixes subcommands with other option types; Discord rejects that", command)
		}
	}

	for _, opt := range options {
		if opt.Name == "" {
			t.Fatalf("command %q has an option missing a name", command)
		}
		if len(opt.Name) > 32 {
			t.Fatalf("option %q in command %q exceeds Discord's 32 character limit", opt.Name, command)
		}
		if opt.Description == "" {
			t.Fatalf("option %q in command %q is missing a description", opt.Name, command)
		}
		if len(opt.Description) > 100 {
			t.Fatalf("description for option %q in command %q exceeds Discord's 100 character limit", opt.Name, command)
		}
		if opt.Autocomplete && opt.Type != discordgo.ApplicationCommandOptionString {
			t.Fatalf("option %q in command %q enables autocomplete but is not a string", opt.Name, command)
		}
		validateOptions(t, command, opt.Options, false)
	}
}

func TestModelCommandUsesSubcommands(t *testing.T) {
	// Regression: /model once carried a top-level "id" option alongside a
	// "reset" subcommand, which Discord rejects outright, so no command was
	// ever published.
	var model *discordgo.ApplicationCommand
	for _, c := range Commands() {
		if c.Name == cmdModel {
			model = c
		}
	}
	if model == nil {
		t.Fatalf("command %q is not published", cmdModel)
	}
	if len(model.Options) != 2 {
		t.Fatalf("/%s has %d options, want the 2 subcommands", cmdModel, len(model.Options))
	}
	for _, opt := range model.Options {
		if opt.Type != discordgo.ApplicationCommandOptionSubCommand {
			t.Fatalf("/%s option %q is type %d, want a subcommand", cmdModel, opt.Name, opt.Type)
		}
	}

	set := findCommandOption(model.Options, cmdModelSet)
	if set == nil {
		t.Fatalf("/%s %s is missing", cmdModel, cmdModelSet)
	}
	id := findCommandOption(set.Options, "id")
	if id == nil {
		t.Fatalf("/%s %s has no id option", cmdModel, cmdModelSet)
	}
	if id.Type != discordgo.ApplicationCommandOptionString || !id.Required || !id.Autocomplete {
		t.Fatalf("id option = %+v, want a required, autocompleting string", id)
	}

	reset := findCommandOption(model.Options, cmdModelReset)
	if reset == nil {
		t.Fatalf("/%s %s is missing", cmdModel, cmdModelReset)
	}
	if len(reset.Options) != 0 {
		t.Fatalf("/%s %s has options, want none: %+v", cmdModel, cmdModelReset, reset.Options)
	}
}

func findCommandOption(options []*discordgo.ApplicationCommandOption, name string) *discordgo.ApplicationCommandOption {
	for _, opt := range options {
		if opt != nil && opt.Name == name {
			return opt
		}
	}
	return nil
}

func TestAdminCommandsAreGated(t *testing.T) {
	// Gating is applied twice: Discord hides these from non-admins via
	// DefaultMemberPermissions, and isAdmin re-checks at runtime because
	// per-guild overrides can re-expose a hidden command.
	adminOnly := map[string]bool{
		cmdSetAIChannel:    true,
		cmdRemoveAIChannel: true,
		cmdListAIChannels:  true,
		cmdProviderStatus:  true,
	}

	for _, c := range Commands() {
		want := adminOnly[c.Name]
		got := c.DefaultMemberPermissions != nil &&
			*c.DefaultMemberPermissions == int64(discordgo.PermissionAdministrator)
		if got != want {
			t.Fatalf("command %q admin-gated = %v, want %v", c.Name, got, want)
		}
	}
}
