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

	if got := optionString(data, "provider"); got != "gemini" {
		t.Fatalf("optionString(provider) = %q, want gemini", got)
	}
	if got := optionString(data, "absent"); got != "" {
		t.Fatalf("optionString(absent) = %q, want empty", got)
	}
	if got := optionString(data, "unset"); got != "" {
		t.Fatalf("optionString(unset) = %q, want empty", got)
	}
	if got := optionString(data, "wrong_type"); got != "" {
		t.Fatalf("optionString(wrong type) = %q, want empty rather than a panic", got)
	}

	if !optionBool(data, "all") {
		t.Fatal("optionBool(all) = false, want true")
	}
	if optionBool(data, "include_history") {
		t.Fatal("optionBool(include_history) = true, want false")
	}
	if optionBool(data, "absent") {
		t.Fatal("optionBool(absent) = true, want false")
	}
	if optionBool(data, "provider") {
		t.Fatal("optionBool on a string option = true, want false")
	}
}

func TestOptionParsingToleratesNilOption(t *testing.T) {
	data := discordgo.ApplicationCommandInteractionData{
		Options: []*discordgo.ApplicationCommandInteractionDataOption{nil},
	}
	if got := optionString(data, "provider"); got != "" {
		t.Fatalf("optionString with a nil option = %q, want empty", got)
	}
	if optionBool(data, "all") {
		t.Fatal("optionBool with a nil option = true, want false")
	}
}

func TestCommandsAreWellFormed(t *testing.T) {
	commands := Commands()
	if len(commands) != 10 {
		t.Fatalf("got %d commands, want 10", len(commands))
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
		for _, opt := range c.Options {
			if opt.Name == "" || opt.Description == "" {
				t.Fatalf("command %q has an option missing a name or description", c.Name)
			}
		}
	}

	for _, name := range []string{
		cmdChat, cmdSetAIChannel, cmdRemoveAIChannel, cmdListAIChannels,
		cmdProviderStatus, cmdSetPreferredModel, cmdClearPreferredModel,
		cmdRemember, cmdMemory, cmdForget,
	} {
		if !seen[name] {
			t.Fatalf("command %q is not published", name)
		}
	}
}

func TestAdminCommandsAreGated(t *testing.T) {
	// Gating is applied twice: Discord hides these from non-admins via
	// DefaultMemberPermissions, and isAdmin re-checks at runtime because
	// per-guild overrides can re-expose a hidden command.
	adminOnly := map[string]bool{
		cmdSetAIChannel:        true,
		cmdRemoveAIChannel:     true,
		cmdListAIChannels:      true,
		cmdProviderStatus:      true,
		cmdSetPreferredModel:   true,
		cmdClearPreferredModel: true,
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
