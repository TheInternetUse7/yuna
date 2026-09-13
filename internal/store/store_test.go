package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	for _, version := range []string{"00001", "00002"} {
		var got string
		if err := first.db.QueryRow(
			`SELECT version FROM schema_migrations WHERE version = ?`, version).Scan(&got); err != nil {
			t.Fatalf("migration %s not recorded: %v", version, err)
		}
	}

	// Every core table must exist, not just schema_migrations.
	for _, table := range []string{"messages", "ai_channels", "model_preferences", "facts", "channel_summaries"} {
		var name string
		err := first.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening must not re-run migration 00001 or fail on existing tables.
	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	var count int
	if err := second.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 2 {
		t.Fatalf("schema_migrations has %d rows after reopen, want 2", count)
	}
}

// Databases created by the previous release carry a preferred_models table.
// Migration 00002 must move those rows into the guild scope and drop it.
func TestMigrationCarriesPreferredModelsForward(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Build the pre-00002 schema by hand, then record only 00001 as applied —
	// the state a database upgraded from the previous release is in.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := db.Exec(migrationsTableSQL); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE preferred_models (
		  guild_id       TEXT PRIMARY KEY,
		  provider_name  TEXT NOT NULL,
		  model_name     TEXT NOT NULL,
		  set_by_user_id TEXT NOT NULL,
		  set_at         INTEGER NOT NULL
		);
		INSERT INTO preferred_models VALUES ('guild-1','gemini','gemini-2.5-flash','admin-1',123);
		INSERT INTO schema_migrations VALUES ('00001', 1);`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	defer s.Close()

	got, err := s.ModelPreference(PreferenceScopeGuild, "guild-1")
	if err != nil {
		t.Fatalf("ModelPreference: %v", err)
	}
	if got == nil {
		t.Fatal("the guild's old preferred model did not survive the migration")
	}
	if got.ProviderName != "gemini" || got.ModelName != "gemini-2.5-flash" || got.SetByUserID != "admin-1" {
		t.Fatalf("migrated preference = %+v", got)
	}
	if got.SetAt == 0 {
		t.Fatal("migrated preference should carry a timestamp")
	}

	var name string
	err = s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'preferred_models'`).Scan(&name)
	if err == nil {
		t.Fatal("preferred_models should have been dropped by migration 00002")
	}
}

func TestMigrationURISurvivesSpacesAndJournalMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestMessageRoundTrip(t *testing.T) {
	s := newTestStore(t)

	base := StoredMessage{
		GuildID:     "guild-1",
		ChannelID:   "chan-1",
		UserID:      "user-1",
		Username:    "alice",
		DisplayName: "Alice",
		Nickname:    "ali",
		Role:        RoleUser,
	}

	var ids []int64
	for i, content := range []string{"first", "second", "third"} {
		m := base
		m.MessageID = "msg-" + string(rune('a'+i))
		m.Content = content
		m.CreatedAt = int64(1000 + i)
		id, err := s.InsertMessage(m)
		if err != nil {
			t.Fatalf("InsertMessage %s: %v", content, err)
		}
		ids = append(ids, id)
	}

	// A duplicate snowflake must not create a second row, and must report the
	// id of the row that already exists.
	dup := base
	dup.MessageID = "msg-a"
	dup.Content = "changed"
	dupID, err := s.InsertMessage(dup)
	if err != nil {
		t.Fatalf("duplicate InsertMessage: %v", err)
	}
	if dupID != ids[0] {
		t.Fatalf("duplicate insert returned id %d, want %d", dupID, ids[0])
	}

	recent, err := s.RecentMessages("chan-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("RecentMessages returned %d rows, want 3", len(recent))
	}
	for i, want := range []string{"first", "second", "third"} {
		if recent[i].Content != want {
			t.Fatalf("RecentMessages[%d].Content = %q, want %q (must be oldest-first)",
				i, recent[i].Content, want)
		}
	}

	// The LIMIT must keep the newest rows, still returned oldest-first.
	limited, err := s.RecentMessages("chan-1", 2)
	if err != nil {
		t.Fatalf("RecentMessages(2): %v", err)
	}
	if len(limited) != 2 || limited[0].Content != "second" || limited[1].Content != "third" {
		t.Fatalf("RecentMessages(2) = %v, want [second third]", limited)
	}

	got, err := s.MessageBySnowflake("chan-1", "msg-b")
	if err != nil {
		t.Fatalf("MessageBySnowflake: %v", err)
	}
	if got == nil || got.Content != "second" {
		t.Fatalf("MessageBySnowflake = %+v, want content second", got)
	}
	missing, err := s.MessageBySnowflake("chan-1", "nope")
	if err != nil {
		t.Fatalf("MessageBySnowflake(missing): %v", err)
	}
	if missing != nil {
		t.Fatalf("MessageBySnowflake(missing) = %+v, want nil", missing)
	}

	since, err := s.MessagesSince("chan-1", ids[0])
	if err != nil {
		t.Fatalf("MessagesSince: %v", err)
	}
	if len(since) != 2 || since[0].Content != "second" || since[1].Content != "third" {
		t.Fatalf("MessagesSince = %v, want [second third] oldest-first", since)
	}

	n, err := s.CountMessagesSince("chan-1", ids[0])
	if err != nil {
		t.Fatalf("CountMessagesSince: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountMessagesSince = %d, want 2", n)
	}

	deleted, err := s.DeleteUserMessages("chan-1", "user-1")
	if err != nil {
		t.Fatalf("DeleteUserMessages: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("DeleteUserMessages deleted %d, want 3", deleted)
	}
}

func TestAIChannels(t *testing.T) {
	s := newTestStore(t)

	is, err := s.IsAIChannel("chan-1")
	if err != nil {
		t.Fatalf("IsAIChannel: %v", err)
	}
	if is {
		t.Fatal("channel should not be an AI channel before it is added")
	}

	if err := s.AddAIChannel("chan-1", "guild-1"); err != nil {
		t.Fatalf("AddAIChannel: %v", err)
	}
	// Adding twice must not error or duplicate.
	if err := s.AddAIChannel("chan-1", "guild-1"); err != nil {
		t.Fatalf("AddAIChannel (again): %v", err)
	}
	if is, err = s.IsAIChannel("chan-1"); err != nil || !is {
		t.Fatalf("IsAIChannel after add = %v, %v; want true", is, err)
	}

	if err := s.RemoveAIChannel("chan-1"); err != nil {
		t.Fatalf("RemoveAIChannel: %v", err)
	}
	if is, err = s.IsAIChannel("chan-1"); err != nil || is {
		t.Fatalf("IsAIChannel after remove = %v, %v; want false", is, err)
	}
}

func TestAIChannelsListsOneGuild(t *testing.T) {
	s := newTestStore(t)

	listed, err := s.AIChannels("guild-1")
	if err != nil {
		t.Fatalf("AIChannels: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("AIChannels on an empty guild = %v, want none", listed)
	}

	for _, id := range []string{"chan-b", "chan-a"} {
		if err := s.AddAIChannel(id, "guild-1"); err != nil {
			t.Fatalf("AddAIChannel(%s): %v", id, err)
		}
	}
	// A channel in another guild must not leak into this guild's list.
	if err := s.AddAIChannel("chan-other", "guild-2"); err != nil {
		t.Fatalf("AddAIChannel(chan-other): %v", err)
	}

	if count, err := s.CountAIChannels(); err != nil || count != 3 {
		t.Fatalf("CountAIChannels = %d, %v; want 3 across all guilds", count, err)
	}

	listed, err = s.AIChannels("guild-1")
	if err != nil {
		t.Fatalf("AIChannels: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("AIChannels = %v, want exactly the two channels of guild-1", listed)
	}
	// added_at has one-second resolution, so these two share a timestamp and
	// the tie is broken by channel_id.
	if listed[0] != "chan-a" || listed[1] != "chan-b" {
		t.Fatalf("AIChannels = %v, want [chan-a chan-b]", listed)
	}

	other, err := s.AIChannels("guild-2")
	if err != nil {
		t.Fatalf("AIChannels(guild-2): %v", err)
	}
	if len(other) != 1 || other[0] != "chan-other" {
		t.Fatalf("AIChannels(guild-2) = %v, want [chan-other]", other)
	}

	if err := s.RemoveAIChannel("chan-a"); err != nil {
		t.Fatalf("RemoveAIChannel: %v", err)
	}
	if listed, err = s.AIChannels("guild-1"); err != nil || len(listed) != 1 || listed[0] != "chan-b" {
		t.Fatalf("AIChannels after remove = %v, %v; want [chan-b]", listed, err)
	}
}

func TestModelPreferenceCRUD(t *testing.T) {
	s := newTestStore(t)

	got, err := s.ModelPreference(PreferenceScopeGuild, "guild-1")
	if err != nil {
		t.Fatalf("ModelPreference: %v", err)
	}
	if got != nil {
		t.Fatalf("ModelPreference before set = %+v, want nil", got)
	}

	if err := s.SetModelPreference(ModelPreference{
		ScopeType: PreferenceScopeGuild, ScopeID: "guild-1",
		ProviderName: "gemini", ModelName: "gemini-2.5-flash", SetByUserID: "user-1",
	}); err != nil {
		t.Fatalf("SetModelPreference: %v", err)
	}
	got, err = s.ModelPreference(PreferenceScopeGuild, "guild-1")
	if err != nil {
		t.Fatalf("ModelPreference: %v", err)
	}
	if got == nil || got.ProviderName != "gemini" || got.ModelName != "gemini-2.5-flash" {
		t.Fatalf("ModelPreference = %+v, want gemini/gemini-2.5-flash", got)
	}
	if got.SetAt == 0 {
		t.Fatal("SetAt should be stamped when the caller leaves it zero")
	}

	// Setting again replaces rather than duplicating.
	if err := s.SetModelPreference(ModelPreference{
		ScopeType: PreferenceScopeGuild, ScopeID: "guild-1",
		ProviderName: "groq", ModelName: "llama-3.3-70b", SetByUserID: "user-2",
	}); err != nil {
		t.Fatalf("SetModelPreference (again): %v", err)
	}
	got, _ = s.ModelPreference(PreferenceScopeGuild, "guild-1")
	if got == nil || got.ProviderName != "groq" || got.SetByUserID != "user-2" {
		t.Fatalf("ModelPreference after replace = %+v, want groq by user-2", got)
	}

	if err := s.ClearModelPreference(PreferenceScopeGuild, "guild-1"); err != nil {
		t.Fatalf("ClearModelPreference: %v", err)
	}
	if got, _ = s.ModelPreference(PreferenceScopeGuild, "guild-1"); got != nil {
		t.Fatalf("ModelPreference after clear = %+v, want nil", got)
	}
}

// Guild IDs and user IDs are both Discord snowflakes, so the same string can
// legitimately appear as a guild scope and as a user scope. The composite key
// is what keeps them apart.
func TestModelPreferenceScopesDoNotCollide(t *testing.T) {
	s := newTestStore(t)
	const shared = "809436401346936842"

	if err := s.SetModelPreference(ModelPreference{
		ScopeType: PreferenceScopeGuild, ScopeID: shared,
		ProviderName: "gemini", ModelName: "gemini-2.5-flash", SetByUserID: "admin",
	}); err != nil {
		t.Fatalf("set guild preference: %v", err)
	}
	if err := s.SetModelPreference(ModelPreference{
		ScopeType: PreferenceScopeDM, ScopeID: shared,
		ProviderName: "groq", ModelName: "llama-3.3-70b", SetByUserID: shared,
	}); err != nil {
		t.Fatalf("set dm preference: %v", err)
	}

	guild, err := s.ModelPreference(PreferenceScopeGuild, shared)
	if err != nil {
		t.Fatalf("guild lookup: %v", err)
	}
	dm, err := s.ModelPreference(PreferenceScopeDM, shared)
	if err != nil {
		t.Fatalf("dm lookup: %v", err)
	}
	if guild == nil || guild.ProviderName != "gemini" {
		t.Fatalf("guild preference = %+v, want gemini", guild)
	}
	if dm == nil || dm.ProviderName != "groq" {
		t.Fatalf("dm preference = %+v, want groq", dm)
	}

	// Clearing one scope must leave the other alone.
	if err := s.ClearModelPreference(PreferenceScopeGuild, shared); err != nil {
		t.Fatalf("clear guild preference: %v", err)
	}
	if dm, _ := s.ModelPreference(PreferenceScopeDM, shared); dm == nil {
		t.Fatal("clearing the guild scope removed the DM scope")
	}
}

func TestFactsDedupCapAndScopes(t *testing.T) {
	s := newTestStore(t)
	guild := Scope{Type: ScopeGuild, ID: "guild-1"}
	dm := Scope{Type: ScopeDM, ID: "dm-1"}

	insert := func(scope Scope, user, content, norm string, at int64) {
		t.Helper()
		err := s.InsertFact(Fact{
			ScopeType: scope.Type, ScopeID: scope.ID, UserID: user,
			Content: content, ContentNorm: norm, Source: SourceCommand, CreatedAt: at,
		})
		if err != nil {
			t.Fatalf("InsertFact %q: %v", content, err)
		}
	}

	insert(guild, "user-1", "Likes tea", "likes tea", 100)
	insert(guild, "user-1", "LIKES   tea", "likes tea", 200) // same norm: deduped
	insert(guild, "user-1", "Lives in Nairobi", "lives in nairobi", 150)
	insert(guild, "user-2", "Likes coffee", "likes coffee", 100)
	insert(dm, "user-1", "Private detail", "private detail", 100)

	n, err := s.CountFacts(guild, "user-1")
	if err != nil {
		t.Fatalf("CountFacts: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountFacts(guild,user-1) = %d, want 2 (duplicate must collapse)", n)
	}

	// Facts are per user: user-2 must not see user-1's facts.
	other, err := s.Facts(guild, "user-2", 10)
	if err != nil {
		t.Fatalf("Facts(user-2): %v", err)
	}
	if len(other) != 1 || other[0].Content != "Likes coffee" {
		t.Fatalf("Facts(user-2) = %+v, want only its own fact", other)
	}

	// Facts are per scope: the DM fact must not appear in the guild.
	guildFacts, err := s.Facts(guild, "user-1", 10)
	if err != nil {
		t.Fatalf("Facts(guild,user-1): %v", err)
	}
	for _, f := range guildFacts {
		if f.Content == "Private detail" {
			t.Fatal("a DM fact leaked into the guild scope")
		}
	}
	// Newest first.
	if len(guildFacts) != 2 || guildFacts[0].Content != "Lives in Nairobi" {
		t.Fatalf("Facts(guild,user-1) = %+v, want newest (Lives in Nairobi) first", guildFacts)
	}

	// The cap drops the oldest rows.
	for i := 0; i < 5; i++ {
		insert(guild, "user-1", "extra", "extra", int64(300+i))
	}
	if err := s.EvictOldestFacts(guild, "user-1", 2); err != nil {
		t.Fatalf("EvictOldestFacts: %v", err)
	}
	kept, err := s.Facts(guild, "user-1", 10)
	if err != nil {
		t.Fatalf("Facts after evict: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("after evict kept %d facts, want 2", len(kept))
	}
	if kept[0].Content != "extra" {
		t.Fatalf("eviction removed the newest fact; got %+v", kept)
	}

	deleted, err := s.DeleteFacts(guild, "user-1", false)
	if err != nil {
		t.Fatalf("DeleteFacts(scope): %v", err)
	}
	if deleted != 2 {
		t.Fatalf("DeleteFacts(scope) deleted %d, want 2", deleted)
	}
	if got, _ := s.Facts(dm, "user-1", 10); len(got) != 1 {
		t.Fatalf("deleting guild facts removed the DM fact: %+v", got)
	}

	deleted, err = s.DeleteFacts(Scope{}, "user-1", true)
	if err != nil {
		t.Fatalf("DeleteFacts(all): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteFacts(all) deleted %d, want 1", deleted)
	}
	if got, _ := s.Facts(dm, "user-1", 10); len(got) != 0 {
		t.Fatalf("facts remain after DeleteFacts(all): %+v", got)
	}
}

func TestChannelSummaryUpsert(t *testing.T) {
	s := newTestStore(t)

	got, err := s.Summary("chan-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got != nil {
		t.Fatalf("Summary before write = %+v, want nil", got)
	}

	if err := s.UpsertSummary(ChannelSummary{
		ChannelID: "chan-1", GuildID: "guild-1", Summary: "first", CoversThroughMessageID: 5,
	}); err != nil {
		t.Fatalf("UpsertSummary: %v", err)
	}
	got, err = s.Summary("chan-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got == nil || got.Summary != "first" || got.CoversThroughMessageID != 5 {
		t.Fatalf("Summary = %+v, want first/5", got)
	}

	if err := s.UpsertSummary(ChannelSummary{
		ChannelID: "chan-1", GuildID: "guild-1", Summary: "second", CoversThroughMessageID: 9,
	}); err != nil {
		t.Fatalf("UpsertSummary (again): %v", err)
	}
	got, _ = s.Summary("chan-1")
	if got == nil || got.Summary != "second" || got.CoversThroughMessageID != 9 {
		t.Fatalf("Summary after upsert = %+v, want second/9", got)
	}
}
