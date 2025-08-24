# database.py
import sqlite3
import logging
from typing import Optional

log = logging.getLogger(__name__)

DB_NAME = "yuna.db"


def initialize_database() -> None:
    """Creates the database and necessary tables if they don't exist."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    # Table for storing API keys for different services
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS api_keys (
            service_name TEXT PRIMARY KEY,
            api_key TEXT NOT NULL
        )
    """
    )
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


def get_api_key(service_name: str) -> Optional[str]:
    """Retrieves an API key for a given service."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("SELECT api_key FROM api_keys WHERE service_name = ?", (service_name,))
    result = cur.fetchone()
    con.close()
    return result[0] if result else None


def set_cooldown(service_name: str, cooldown_seconds: int) -> None:
    """Sets a cooldown (seconds from now) for a specific service.

    Args:
        service_name: Provider identifier (e.g., 'groq').
        cooldown_seconds: Number of seconds from now to block the provider.
    """
    import time

    cooldown_until = int(time.time()) + int(cooldown_seconds)
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "INSERT OR REPLACE INTO rate_limit_cooldowns (service_name, cooldown_until) VALUES (?, ?)",
        (service_name, cooldown_until),
    )
    con.commit()
    con.close()
    log.info("Set cooldown for %s until %s", service_name, cooldown_until)


def is_on_cooldown(service_name: str) -> bool:
    """Returns True if the given service is currently on cooldown."""
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
        try:
            return time.time() < float(result[0])
        except Exception:
            return False
    return False


# You can add a function to insert keys manually for now
def add_api_key(service_name: str, api_key: str) -> None:
    """Stores or updates an API key for a provider."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "INSERT OR REPLACE INTO api_keys (service_name, api_key) VALUES (?, ?)",
        (service_name, api_key),
    )
    con.commit()
    con.close()
    log.info("API key stored for service %s", service_name)
