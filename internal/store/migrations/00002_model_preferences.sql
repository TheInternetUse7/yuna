-- Model selection, per scope.
--
-- Replaces preferred_models, which was guild-only and pinned a provider to the
-- front of the chain. A preference now names a provider AND one of its models,
-- and it applies either to a guild or to a person's DMs.
--
-- scope_id holds a guild ID when scope_type is 'guild', and a user ID when it
-- is 'dm'. Both are Discord snowflakes drawn from the same numeric space, so
-- the composite primary key is what keeps a user ID from shadowing a guild ID.
-- DM preferences follow the person across every DM with the bot.

CREATE TABLE IF NOT EXISTS model_preferences (
  scope_type     TEXT NOT NULL CHECK (scope_type IN ('guild','dm')),
  scope_id       TEXT NOT NULL,
  provider_name  TEXT NOT NULL,
  model_name     TEXT NOT NULL,
  set_by_user_id TEXT NOT NULL,
  set_at         INTEGER NOT NULL,
  PRIMARY KEY (scope_type, scope_id)
);

-- Carry existing guild overrides across. set_at is dropped rather than
-- preserved: the column exists for auditing, and a fresh timestamp here is
-- more honest than implying the row was just written at its original time.
INSERT OR IGNORE INTO model_preferences
  (scope_type, scope_id, provider_name, model_name, set_by_user_id, set_at)
SELECT 'guild', guild_id, provider_name, model_name, set_by_user_id, unixepoch()
FROM preferred_models;

DROP TABLE IF EXISTS preferred_models;
