// Package store owns Yuna's SQLite persistence: message history, AI channels,
// per-scope model selections, per-user facts and channel summaries.
//
// Discord snowflakes are stored as TEXT because they exceed the range of a
// signed 64-bit integer in some code paths and never need arithmetic here.
// Timestamps are Unix seconds.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Scope kinds for facts.
const (
	ScopeDM    = "dm"
	ScopeGuild = "guild"
)

// Scope kinds for model preferences. They reuse the fact vocabulary so the two
// features read the same way, but a preference's scope ID means something
// slightly different: for ScopeDM it is the user's ID, not a channel ID,
// because a model choice follows the person across every DM.
const (
	PreferenceScopeDM    = ScopeDM
	PreferenceScopeGuild = ScopeGuild
)

// Roles stored in messages.role.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Provenance recorded on every fact.
const (
	SourceCommand = "command"
	SourceTool    = "tool"
	SourceSummary = "summary"
)

// Scope identifies the namespace a fact belongs to. Guild facts follow the
// user across every channel in that guild; a DM is its own private scope.
type Scope struct {
	Type string // ScopeDM or ScopeGuild
	ID   string // guild ID, or DM channel ID
}

// StoredMessage is one row of conversation history.
type StoredMessage struct {
	ID               int64
	MessageID        string
	GuildID          string // empty for DMs
	ChannelID        string
	UserID           string
	Username         string
	DisplayName      string
	Nickname         string
	Role             string // RoleUser or RoleAssistant
	Content          string
	ReplyToMessageID string
	CreatedAt        int64
}

// Fact is a single remembered statement about one user within one scope.
type Fact struct {
	ID          int64
	ScopeType   string
	ScopeID     string
	UserID      string
	Content     string
	ContentNorm string
	Source      string
	CreatedAt   int64
}

// ChannelSummary is the rolling summary of one channel's older conversation.
type ChannelSummary struct {
	ChannelID              string
	GuildID                string
	Summary                string
	CoversThroughMessageID int64
	UpdatedAt              int64
}

// ModelPreference is a stored model choice: which provider and model should
// answer, for one guild or for one person's DMs.
type ModelPreference struct {
	ScopeType    string // PreferenceScopeGuild or PreferenceScopeDM
	ScopeID      string // guild ID, or user ID for DMs
	ProviderName string
	ModelName    string
	SetByUserID  string
	SetAt        int64
}

// Store wraps the SQLite handle. The connection pool is capped at one writer,
// which removes lock contention entirely at Discord's message rate.
type Store struct {
	db *sql.DB
}

// Open creates or opens the database at path and applies the schema.
func Open(path string) (*Store, error) {
	dsn, err := dataSourceName(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.applyMigrations(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate %q: %w", path, err)
	}
	return s, nil
}

// dataSourceName builds a file: URI for the driver.
//
// The path must be absolute and slash-separated: the driver hands any DSN
// beginning with "file:" straight to SQLite's URI parser, where a bare Windows
// path such as "C:\data\yuna.db" would have its drive letter read as a host.
// Building the URI through net/url also escapes spaces and other characters
// that would otherwise truncate the path.
func dataSourceName(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve database path %q: %w", path, err)
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}

	uri := url.URL{Scheme: "file", Path: slashed}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	uri.RawQuery = query.Encode()
	return uri.String(), nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// The ordered migration files and schema_migrations live in migrate.go.
const messageColumns = `id, message_id, guild_id, channel_id, user_id, username,
	display_name, nickname, role, content, reply_to_message_id, created_at`

func scanMessage(sc interface{ Scan(...any) error }) (StoredMessage, error) {
	var m StoredMessage
	err := sc.Scan(&m.ID, &m.MessageID, &m.GuildID, &m.ChannelID, &m.UserID, &m.Username,
		&m.DisplayName, &m.Nickname, &m.Role, &m.Content, &m.ReplyToMessageID, &m.CreatedAt)
	return m, err
}

// InsertMessage stores one message and returns its local row id. Re-inserting a
// message that is already stored is a no-op and returns the existing row id.
func (s *Store) InsertMessage(m StoredMessage) (int64, error) {
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().Unix()
	}
	res, err := s.db.Exec(`INSERT INTO messages
			(message_id, guild_id, channel_id, user_id, username, display_name, nickname,
			 role, content, reply_to_message_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO NOTHING`,
		m.MessageID, m.GuildID, m.ChannelID, m.UserID, m.Username, m.DisplayName, m.Nickname,
		m.Role, m.Content, m.ReplyToMessageID, m.CreatedAt)
	if err != nil {
		return 0, fmt.Errorf("insert message %s: %w", m.MessageID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		if existing, err := s.MessageBySnowflake(m.ChannelID, m.MessageID); err == nil && existing != nil {
			return existing.ID, nil
		}
		return 0, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert message %s: %w", m.MessageID, err)
	}
	return id, nil
}

// RecentMessages returns up to limit messages for a channel, oldest first.
func (s *Store) RecentMessages(channelID string, limit int) ([]StoredMessage, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT `+messageColumns+`
		FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent messages for %s: %w", channelID, err)
	}
	defer rows.Close()

	var out []StoredMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("recent messages for %s: %w", channelID, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent messages for %s: %w", channelID, err)
	}
	// Queried newest-first so the LIMIT kept the most recent; flip to
	// chronological order for the model.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// MessageBySnowflake looks up one stored message by its Discord ID.
func (s *Store) MessageBySnowflake(channelID, messageID string) (*StoredMessage, error) {
	row := s.db.QueryRow(`SELECT `+messageColumns+`
		FROM messages WHERE channel_id = ? AND message_id = ?`, channelID, messageID)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("message %s: %w", messageID, err)
	}
	return &m, nil
}

// MessagesSince returns messages in a channel with a local id greater than
// localID, oldest first. Used to feed summary refreshes.
func (s *Store) MessagesSince(channelID string, localID int64) ([]StoredMessage, error) {
	rows, err := s.db.Query(`SELECT `+messageColumns+`
		FROM messages WHERE channel_id = ? AND id > ? ORDER BY id ASC`, channelID, localID)
	if err != nil {
		return nil, fmt.Errorf("messages since %d for %s: %w", localID, channelID, err)
	}
	defer rows.Close()

	var out []StoredMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("messages since %d for %s: %w", localID, channelID, err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountMessagesSince counts messages in a channel newer than localID.
func (s *Store) CountMessagesSince(channelID string, localID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE channel_id = ? AND id > ?`, channelID, localID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count messages since %d for %s: %w", localID, channelID, err)
	}
	return n, nil
}

// DeleteUserMessages removes one user's stored messages from one channel and
// reports how many rows went away.
func (s *Store) DeleteUserMessages(channelID, userID string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM messages WHERE channel_id = ? AND user_id = ?`, channelID, userID)
	if err != nil {
		return 0, fmt.Errorf("delete messages for %s in %s: %w", userID, channelID, err)
	}
	return res.RowsAffected()
}

// AddAIChannel marks a channel as one Yuna answers every message in.
func (s *Store) AddAIChannel(channelID, guildID string) error {
	_, err := s.db.Exec(`INSERT INTO ai_channels (channel_id, guild_id, added_at)
		VALUES (?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET guild_id = excluded.guild_id`,
		channelID, guildID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("add ai channel %s: %w", channelID, err)
	}
	return nil
}

// RemoveAIChannel unmarks a channel.
func (s *Store) RemoveAIChannel(channelID string) error {
	_, err := s.db.Exec(`DELETE FROM ai_channels WHERE channel_id = ?`, channelID)
	if err != nil {
		return fmt.Errorf("remove ai channel %s: %w", channelID, err)
	}
	return nil
}

// IsAIChannel reports whether every message in the channel is a trigger.
func (s *Store) IsAIChannel(channelID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM ai_channels WHERE channel_id = ?)`, channelID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check ai channel %s: %w", channelID, err)
	}
	return exists, nil
}

// AIChannels lists the channels in one guild that Yuna answers every message
// in, oldest first. Ties are broken by id: added_at has one-second resolution,
// so several channels can share a timestamp.
func (s *Store) AIChannels(guildID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT channel_id FROM ai_channels WHERE guild_id = ? ORDER BY added_at, channel_id`,
		guildID)
	if err != nil {
		return nil, fmt.Errorf("list ai channels for guild %s: %w", guildID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list ai channels for guild %s: %w", guildID, err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CountAIChannels reports how many AI channels exist across every guild, for
// the startup log: a bot that silently answers nothing is usually a bot with no
// channels registered.
func (s *Store) CountAIChannels() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ai_channels`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count ai channels: %w", err)
	}
	return count, nil
}

// SetModelPreference records a model choice for one scope, replacing any
// previous choice in that scope.
func (s *Store) SetModelPreference(p ModelPreference) error {
	if p.SetAt == 0 {
		p.SetAt = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO model_preferences
			(scope_type, scope_id, provider_name, model_name, set_by_user_id, set_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope_type, scope_id) DO UPDATE SET
			provider_name  = excluded.provider_name,
			model_name     = excluded.model_name,
			set_by_user_id = excluded.set_by_user_id,
			set_at         = excluded.set_at`,
		p.ScopeType, p.ScopeID, p.ProviderName, p.ModelName, p.SetByUserID, p.SetAt)
	if err != nil {
		return fmt.Errorf("set model preference for %s %s: %w", p.ScopeType, p.ScopeID, err)
	}
	return nil
}

// ModelPreference returns the stored choice for one scope, or nil when the
// scope has none.
func (s *Store) ModelPreference(scopeType, scopeID string) (*ModelPreference, error) {
	var p ModelPreference
	err := s.db.QueryRow(`SELECT scope_type, scope_id, provider_name, model_name, set_by_user_id, set_at
		FROM model_preferences WHERE scope_type = ? AND scope_id = ?`, scopeType, scopeID).
		Scan(&p.ScopeType, &p.ScopeID, &p.ProviderName, &p.ModelName, &p.SetByUserID, &p.SetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("model preference for %s %s: %w", scopeType, scopeID, err)
	}
	return &p, nil
}

// ClearModelPreference removes one scope's choice.
func (s *Store) ClearModelPreference(scopeType, scopeID string) error {
	_, err := s.db.Exec(`DELETE FROM model_preferences WHERE scope_type = ? AND scope_id = ?`,
		scopeType, scopeID)
	if err != nil {
		return fmt.Errorf("clear model preference for %s %s: %w", scopeType, scopeID, err)
	}
	return nil
}

// InsertFact stores a fact for one user in one scope. Exact duplicates (after
// normalisation) are silently ignored by the UNIQUE constraint.
func (s *Store) InsertFact(f Fact) error {
	if f.CreatedAt == 0 {
		f.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO facts
			(scope_type, scope_id, user_id, content, content_norm, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope_type, scope_id, user_id, content_norm) DO NOTHING`,
		f.ScopeType, f.ScopeID, f.UserID, f.Content, f.ContentNorm, f.Source, f.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert fact for %s: %w", f.UserID, err)
	}
	return nil
}

// Facts returns one user's facts in one scope, newest first.
func (s *Store) Facts(scope Scope, userID string, limit int) ([]Fact, error) {
	query := `SELECT id, scope_type, scope_id, user_id, content, content_norm, source, created_at
		FROM facts WHERE scope_type = ? AND scope_id = ? AND user_id = ?
		ORDER BY created_at DESC, id DESC`
	args := []any{scope.Type, scope.ID, userID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("facts for %s: %w", userID, err)
	}
	defer rows.Close()

	var out []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.ScopeType, &f.ScopeID, &f.UserID,
			&f.Content, &f.ContentNorm, &f.Source, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("facts for %s: %w", userID, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CountFacts counts one user's facts in one scope.
func (s *Store) CountFacts(scope Scope, userID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM facts
		WHERE scope_type = ? AND scope_id = ? AND user_id = ?`,
		scope.Type, scope.ID, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count facts for %s: %w", userID, err)
	}
	return n, nil
}

// EvictOldestFacts trims a user's facts in one scope down to keep, dropping the
// least recently stored ones first.
func (s *Store) EvictOldestFacts(scope Scope, userID string, keep int) error {
	if keep < 0 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM facts WHERE id IN (
			SELECT id FROM facts
			WHERE scope_type = ? AND scope_id = ? AND user_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT -1 OFFSET ?
		)`, scope.Type, scope.ID, userID, keep)
	if err != nil {
		return fmt.Errorf("evict facts for %s: %w", userID, err)
	}
	return nil
}

// DeleteFacts removes a user's facts. When all is true the scope is ignored and
// every fact that user owns is deleted, in every scope.
func (s *Store) DeleteFacts(scope Scope, userID string, all bool) (int64, error) {
	var (
		res sql.Result
		err error
	)
	if all {
		res, err = s.db.Exec(`DELETE FROM facts WHERE user_id = ?`, userID)
	} else {
		res, err = s.db.Exec(`DELETE FROM facts
			WHERE scope_type = ? AND scope_id = ? AND user_id = ?`, scope.Type, scope.ID, userID)
	}
	if err != nil {
		return 0, fmt.Errorf("delete facts for %s: %w", userID, err)
	}
	return res.RowsAffected()
}

// Summary returns a channel's rolling summary, or nil when none exists yet.
func (s *Store) Summary(channelID string) (*ChannelSummary, error) {
	var c ChannelSummary
	err := s.db.QueryRow(`SELECT channel_id, guild_id, summary, covers_through_message_id, updated_at
		FROM channel_summaries WHERE channel_id = ?`, channelID).
		Scan(&c.ChannelID, &c.GuildID, &c.Summary, &c.CoversThroughMessageID, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("summary for %s: %w", channelID, err)
	}
	return &c, nil
}

// UpsertSummary writes a channel's rolling summary.
func (s *Store) UpsertSummary(c ChannelSummary) error {
	if c.UpdatedAt == 0 {
		c.UpdatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO channel_summaries
			(channel_id, guild_id, summary, covers_through_message_id, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			guild_id                  = excluded.guild_id,
			summary                   = excluded.summary,
			covers_through_message_id = excluded.covers_through_message_id,
			updated_at                = excluded.updated_at`,
		c.ChannelID, c.GuildID, c.Summary, c.CoversThroughMessageID, c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert summary for %s: %w", c.ChannelID, err)
	}
	return nil
}
