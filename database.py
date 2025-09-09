import sqlite3
import logging
from typing import Optional

log = logging.getLogger(__name__)

DB_NAME = "yuna.db"


def initialize_database() -> None:
    """Creates the database and necessary tables if they don't exist."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()

    # Table for storing designated AI channels
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS ai_channels (
            channel_id INTEGER PRIMARY KEY,
            guild_id INTEGER NOT NULL
        )
    """
    )

    # Table for tracking rate limit cooldowns
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS rate_limit_cooldowns (
            service_name TEXT PRIMARY KEY,
            cooldown_until INTEGER NOT NULL
        )
    """
    )

    # Table for storing preferred model overrides per guild
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS preferred_models (
            guild_id INTEGER PRIMARY KEY,
            provider_name TEXT NOT NULL,
            model_name TEXT NOT NULL,
            set_by_user_id INTEGER NOT NULL,
            set_at INTEGER NOT NULL
        )
    """
    )

    con.commit()
    con.close()
    log.info("Database initialized.")


def add_ai_channel(channel_id: int, guild_id: int) -> None:
    """Adds a channel to the list of AI-enabled channels."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "INSERT OR REPLACE INTO ai_channels (channel_id, guild_id) VALUES (?, ?)",
        (channel_id, guild_id),
    )
    con.commit()
    con.close()


def remove_ai_channel(channel_id: int) -> None:
    """Removes a channel from the list of AI-enabled channels."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("DELETE FROM ai_channels WHERE channel_id = ?", (channel_id,))
    con.commit()
    con.close()


def is_ai_channel(channel_id: int) -> bool:
    """Checks if a channel is an AI-enabled channel."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("SELECT 1 FROM ai_channels WHERE channel_id = ?", (channel_id,))
    result = cur.fetchone()
    con.close()
    return result is not None


def set_cooldown(service_name: str, cooldown_seconds: int) -> None:
    """Sets a cooldown for a specific service."""
    import time

    cooldown_until = int(time.time()) + cooldown_seconds
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "INSERT OR REPLACE INTO rate_limit_cooldowns (service_name, cooldown_until) VALUES (?, ?)",
        (service_name, cooldown_until),
    )
    con.commit()
    con.close()
    log.info(
        f"Set cooldown for {service_name} until {cooldown_until} ({cooldown_seconds}s)"
    )


def is_on_cooldown(service_name: str) -> bool:
    """Checks if a service is currently on cooldown."""
    import time

    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "SELECT cooldown_until FROM rate_limit_cooldowns WHERE service_name = ?",
        (service_name,),
    )
    result = cur.fetchone()
    con.close()
    if result:
        is_cooled = time.time() < result[0]
        if not is_cooled:
            # Cooldown has expired, clean up the record
            clear_expired_cooldown(service_name)
        return is_cooled
    return False


def clear_expired_cooldown(service_name: str) -> None:
    """Removes expired cooldown records."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "DELETE FROM rate_limit_cooldowns WHERE service_name = ?", (service_name,)
    )
    con.commit()
    con.close()
    log.debug(f"Cleared expired cooldown for {service_name}")


def get_cooldown_status() -> dict:
    """Returns the current cooldown status for all services."""
    import time

    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("SELECT service_name, cooldown_until FROM rate_limit_cooldowns")
    results = cur.fetchall()
    con.close()

    current_time = time.time()
    status = {}
    for service_name, cooldown_until in results:
        if current_time < cooldown_until:
            status[service_name] = {
                "on_cooldown": True,
                "remaining_seconds": int(cooldown_until - current_time),
            }
        else:
            status[service_name] = {"on_cooldown": False, "remaining_seconds": 0}

    return status


def set_preferred_model(
    guild_id: int, provider_name: str, model_name: str, user_id: int
) -> None:
    """Sets a preferred model override for a specific guild."""
    import time

    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "INSERT OR REPLACE INTO preferred_models (guild_id, provider_name, model_name, set_by_user_id, set_at) VALUES (?, ?, ?, ?, ?)",
        (guild_id, provider_name, model_name, user_id, int(time.time())),
    )
    con.commit()
    con.close()
    log.info(f"Set preferred model for guild {guild_id}: {provider_name}/{model_name}")


def get_preferred_model(guild_id: int) -> Optional[dict]:
    """Gets the preferred model override for a specific guild."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "SELECT provider_name, model_name, set_by_user_id, set_at FROM preferred_models WHERE guild_id = ?",
        (guild_id,),
    )
    result = cur.fetchone()
    con.close()

    if result:
        return {
            "provider_name": result[0],
            "model_name": result[1],
            "set_by_user_id": result[2],
            "set_at": result[3],
        }
    return None


def clear_preferred_model(guild_id: int) -> None:
    """Clears the preferred model override for a specific guild."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("DELETE FROM preferred_models WHERE guild_id = ?", (guild_id,))
    con.commit()
    con.close()
    log.info(f"Cleared preferred model for guild {guild_id}")
