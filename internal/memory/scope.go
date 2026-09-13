// Package memory owns Yuna's long-term memory: per-user facts and rolling
// per-channel summaries.
//
// Isolation is a bot-side invariant, never a prompt instruction. Every fact is
// keyed by (scope, user), and the only way a fact reaches a model is through
// Manager.Context, which derives the scope from the current turn and filters by
// the current author. Facts belonging to another user or another scope are
// never placed in the message list, so the model cannot leak what it was never
// given.
package memory

import "github.com/TheInternetUse7/yuna/internal/store"

// Scope is the namespace a fact lives in: one guild, or one DM conversation.
type Scope = store.Scope

// GuildScope is the scope shared by every channel of one guild.
func GuildScope(guildID string) Scope {
	return Scope{Type: store.ScopeGuild, ID: guildID}
}

// DMScope is the private scope of one DM conversation. A DM channel is unique
// per user, so DM facts are inherently private to that user.
func DMScope(channelID string) Scope {
	return Scope{Type: store.ScopeDM, ID: channelID}
}

// Turn describes the message Yuna is answering right now.
type Turn struct {
	GuildID     string
	ChannelID   string
	UserID      string
	Username    string
	DisplayName string
	Nickname    string
}

// Scope derives the namespace of the turn.
func (t Turn) Scope() Scope {
	if t.GuildID == "" {
		return DMScope(t.ChannelID)
	}
	return GuildScope(t.GuildID)
}

// Belongs reports whether a stored fact is this turn's own: same scope and same
// author. This is the single definition of the isolation rule.
func Belongs(f store.Fact, scope Scope, userID string) bool {
	return f.ScopeType == scope.Type && f.ScopeID == scope.ID && f.UserID == userID
}
