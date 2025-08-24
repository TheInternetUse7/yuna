# database.py
import sqlite3

DB_NAME = "yuna.db"


def initialize_database():
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
    con.commit()
    con.close()
    print("Database initialized.")


def add_ai_channel(channel_id, guild_id):
    """Adds a channel to the list of AI-enabled channels."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "INSERT OR REPLACE INTO ai_channels (channel_id, guild_id) VALUES (?, ?)",
        (channel_id, guild_id),
    )
    con.commit()
    con.close()


def remove_ai_channel(channel_id):
    """Removes a channel from the list of AI-enabled channels."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("DELETE FROM ai_channels WHERE channel_id = ?", (channel_id,))
    con.commit()
    con.close()


def is_ai_channel(channel_id):
    """Checks if a channel is an AI-enabled channel."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("SELECT 1 FROM ai_channels WHERE channel_id = ?", (channel_id,))
    result = cur.fetchone()
    con.close()
    return result is not None


def get_api_key(service_name):
    """Retrieves an API key for a given service."""
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute("SELECT api_key FROM api_keys WHERE service_name = ?", (service_name,))
    result = cur.fetchone()
    con.close()
    return result[0] if result else None


# You can add a function to insert keys manually for now
def add_api_key(service_name, api_key):
    con = sqlite3.connect(DB_NAME)
    cur = con.cursor()
    cur.execute(
        "INSERT OR REPLACE INTO api_keys (service_name, api_key) VALUES (?, ?)",
        (service_name, api_key),
    )
    con.commit()
    con.close()
