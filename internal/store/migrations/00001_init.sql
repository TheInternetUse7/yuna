-- Initial Yuna schema.

CREATE TABLE IF NOT EXISTS messages (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id          TEXT NOT NULL UNIQUE,
  guild_id            TEXT NOT NULL DEFAULT '',
  channel_id          TEXT NOT NULL,
  user_id             TEXT NOT NULL,
  username            TEXT NOT NULL,
  display_name        TEXT NOT NULL,
  nickname            TEXT NOT NULL,
  role                TEXT NOT NULL,
  content             TEXT NOT NULL,
  reply_to_message_id TEXT NOT NULL DEFAULT '',
  created_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_messages_message_id ON messages(message_id);

CREATE TABLE IF NOT EXISTS ai_channels (
  channel_id TEXT PRIMARY KEY,
  guild_id   TEXT NOT NULL,
  added_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS preferred_models (
  guild_id       TEXT PRIMARY KEY,
  provider_name  TEXT NOT NULL,
  model_name     TEXT NOT NULL,
  set_by_user_id TEXT NOT NULL,
  set_at         INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS facts (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  scope_type   TEXT NOT NULL CHECK (scope_type IN ('dm','guild')),
  scope_id     TEXT NOT NULL,
  user_id      TEXT NOT NULL,
  content      TEXT NOT NULL,
  content_norm TEXT NOT NULL,
  source       TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  UNIQUE (scope_type, scope_id, user_id, content_norm)
);
CREATE INDEX IF NOT EXISTS idx_facts_lookup ON facts(scope_type, scope_id, user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS channel_summaries (
  channel_id                TEXT PRIMARY KEY,
  guild_id                  TEXT NOT NULL DEFAULT '',
  summary                   TEXT NOT NULL,
  covers_through_message_id INTEGER NOT NULL,
  updated_at                INTEGER NOT NULL
);
