import os
import logging
import discord
from discord.ext import commands
from dotenv import load_dotenv
import database
import ai_manager

# Load environment variables and logging
load_dotenv()

# Configuration Constants
AI_CHANNEL_MESSAGE_LIMIT = 15  # Number of messages to fetch for AI channel context
REPLY_CHAIN_LIMIT = 15  # Maximum number of messages to follow in reply chains
LOG_FILE_MAX_SIZE = 5 * 1024 * 1024  # 5MB max log file size
LOG_BACKUP_COUNT = 3  # Number of backup log files to keep

# The bot will automatically fallback between providers when rate limits or errors occur

# Configure logging with rotation and proper encoding
from logging.handlers import RotatingFileHandler
import sys

# Create formatters
file_formatter = logging.Formatter(
    "%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
console_formatter = logging.Formatter("%(levelname)s - %(name)s - %(message)s")

# Create handlers
file_handler = RotatingFileHandler(
    "yuna.log",
    maxBytes=LOG_FILE_MAX_SIZE,
    backupCount=LOG_BACKUP_COUNT,
    encoding="utf-8",
)
file_handler.setLevel(logging.INFO)  # Only INFO and above to file
file_handler.setFormatter(file_formatter)

console_handler = logging.StreamHandler(sys.stdout)
console_handler.setLevel(logging.INFO)  # Only INFO and above to console
console_handler.setFormatter(console_formatter)

# Configure root logger
logging.basicConfig(level=logging.DEBUG, handlers=[file_handler, console_handler])

log = logging.getLogger(__name__)

DISCORD_TOKEN = os.getenv("DISCORD_TOKEN")

# Bot setup
intents = discord.Intents.default()
intents.message_content = True
intents.members = True
bot = commands.Bot(command_prefix="/", intents=intents)


@bot.event
async def on_ready():
    """Event that fires when the bot is connected and ready."""
    log.info(f"{bot.user} has connected to Discord!")
    log.info("Initializing database...")
    database.initialize_database()
    log.info("Syncing slash commands...")
    await bot.tree.sync()
    log.info("Yuna is ready.")


# --- Helper function for context gathering ---
async def gather_context(message: discord.Message, bot: commands.Bot):
    """Analyzes a message and returns a formatted conversation history."""
    history = []

    # Helper to create the author details dictionary
    def get_author_details(author: discord.Member | discord.User):
        return {
            "username": author.name,
            "display_name": author.display_name,
            "server_nickname": author.nick or author.display_name,
        }

    # Case C: AI Channel
    if database.is_ai_channel(message.channel.id):
        log.info(f"Processing message in AI channel: {message.channel.name}")
        # Fetch last N messages, reverse them for chronological order
        channel_messages = [
            msg async for msg in message.channel.history(limit=AI_CHANNEL_MESSAGE_LIMIT)
        ]
        channel_messages.reverse()
        for msg in channel_messages:
            role = "assistant" if msg.author == bot.user else "user"
            if msg.content:
                entry = {"role": role, "content": msg.content}
                if role == "user":
                    entry["author_details"] = get_author_details(msg.author)
                history.append(entry)
        log.info(f"AI Channel: Gathered {len(history)} messages for context")
        return history

    # Determine if the bot should respond (mention or reply)
    is_mention = bot.user in message.mentions
    is_reply_to_bot = (
        message.reference
        and message.reference.resolved
        and message.reference.resolved.author == bot.user
    )

    if not (is_mention or is_reply_to_bot):
        return None  # Bot should not respond

    # Log only when we will respond
    log.info(f"Bot will respond - Mention: {is_mention}, Reply: {is_reply_to_bot}")

    # Case B: Reply Chain
    if is_reply_to_bot:
        log.info("Processing reply chain")
        current_message = message
        # Walk up the reply chain
        for i in range(REPLY_CHAIN_LIMIT):
            role = "assistant" if current_message.author == bot.user else "user"
            if current_message.content:
                entry = {"role": role, "content": current_message.content}
                if role == "user":
                    entry["author_details"] = get_author_details(current_message.author)
                history.insert(0, entry)  # Prepend to keep chronological order

            if not current_message.reference:
                break

            # Try to get the referenced message
            try:
                if current_message.reference.resolved:
                    # Use the resolved message if available
                    current_message = current_message.reference.resolved
                else:
                    # Fetch the message if not cached
                    current_message = await message.channel.fetch_message(
                        current_message.reference.message_id
                    )
            except (discord.NotFound, discord.HTTPException) as e:
                log.debug(f"Could not fetch referenced message: {e}")
                break  # Stop if we can't find the message

        log.info(f"Reply Chain: Gathered {len(history)} messages for context")
        return history

    # Case A: Direct Mention
    if is_mention:
        log.info("Processing direct mention")
        prompt = (
            message.content.replace(f"<@{bot.user.id}>", "")
            .replace(f"<@!{bot.user.id}>", "")
            .strip()
        )

        if prompt:
            history.append(
                {
                    "role": "user",
                    "content": prompt,
                    "author_details": get_author_details(message.author),
                }
            )
            log.info(f"Direct Mention: Gathered 1 message for context")
        else:
            # Even if there's no text after the mention, we should still respond
            history.append(
                {
                    "role": "user",
                    "content": "Hello!",  # Default greeting when just mentioned
                    "author_details": get_author_details(message.author),
                }
            )
            log.info("Direct Mention: No text after mention, using default greeting")
        return history

    return None


# --- Admin Commands ---
@bot.tree.command(
    name="set_ai_channel",
    description="Sets the current channel as an AI chat channel.",
)
@discord.app_commands.checks.has_permissions(administrator=True)
async def set_ai_channel(interaction: discord.Interaction):
    await interaction.response.defer(ephemeral=True)
    channel_id = interaction.channel_id
    guild_id = interaction.guild_id
    database.add_ai_channel(channel_id, guild_id)
    await interaction.followup.send(f"Channel <#{channel_id}> is now an AI channel.")


@bot.tree.command(
    name="remove_ai_channel",
    description="Removes the current channel from the AI chat list.",
)
@discord.app_commands.checks.has_permissions(administrator=True)
async def remove_ai_channel(interaction: discord.Interaction):
    await interaction.response.defer(ephemeral=True)
    channel_id = interaction.channel_id
    database.remove_ai_channel(channel_id)
    await interaction.followup.send(
        f"Channel <#{channel_id}> is no longer an AI channel."
    )


@bot.tree.command(
    name="provider_status",
    description="Shows the status of all AI providers and their availability.",
)
@discord.app_commands.checks.has_permissions(administrator=True)
async def provider_status(interaction: discord.Interaction):
    await interaction.response.defer(ephemeral=True)

    try:
        status_summary = ai_manager.get_provider_summary()

        # Add preferred model info if set
        if interaction.guild_id:
            preferred = database.get_preferred_model(interaction.guild_id)
            if preferred:
                status_summary += f"\n\n🎯 **Preferred Model Override**: {preferred['provider_name']}/{preferred['model_name']}"

        await interaction.followup.send(status_summary)
    except Exception as e:
        log.error(f"Error getting provider status: {e}", exc_info=True)
        await interaction.followup.send(
            "Error retrieving provider status. Check logs for details."
        )


@bot.tree.command(
    name="set_preferred_model",
    description="Set a preferred AI model for this server (overrides automatic selection).",
)
@discord.app_commands.describe(
    provider="The provider name (gemini, openrouter, cerebras, groq, cohere, together_ai)",
    model="The specific model name (optional - uses default for provider if not specified)",
)
@discord.app_commands.checks.has_permissions(administrator=True)
async def set_preferred_model(
    interaction: discord.Interaction, provider: str, model: str = None
):
    await interaction.response.defer(ephemeral=True)

    try:
        # Validate provider exists
        valid_providers = {p["name"]: p for p in ai_manager.PROVIDER_PRIORITY}

        if provider.lower() not in valid_providers:
            available = ", ".join(valid_providers.keys())
            await interaction.followup.send(
                f"❌ Invalid provider. Available providers: {available}"
            )
            return

        # Use default model for provider if not specified
        provider_info = valid_providers[provider.lower()]
        final_model = model if model else provider_info["model"]

        # Set the preferred model
        database.set_preferred_model(
            interaction.guild_id, provider.lower(), final_model, interaction.user.id
        )

        await interaction.followup.send(
            f"✅ Set preferred model for this server: **{provider.lower()}**\n"
            f"Model: `{final_model}`\n\n"
            f"The bot will now prioritize this model for all responses in this server. "
            f"Use `/clear_preferred_model` to return to automatic selection."
        )

    except Exception as e:
        log.error(f"Error setting preferred model: {e}", exc_info=True)
        await interaction.followup.send(
            "Error setting preferred model. Check logs for details."
        )


@bot.tree.command(
    name="clear_preferred_model",
    description="Clear the preferred model override and return to automatic selection.",
)
@discord.app_commands.checks.has_permissions(administrator=True)
async def clear_preferred_model(interaction: discord.Interaction):
    await interaction.response.defer(ephemeral=True)

    try:
        preferred = database.get_preferred_model(interaction.guild_id)
        if not preferred:
            await interaction.followup.send(
                "❌ No preferred model is currently set for this server."
            )
            return

        database.clear_preferred_model(interaction.guild_id)
        await interaction.followup.send(
            f"✅ Cleared preferred model override.\n"
            f"The bot will now use automatic provider selection based on availability and intelligence scores."
        )

    except Exception as e:
        log.error(f"Error clearing preferred model: {e}", exc_info=True)
        await interaction.followup.send(
            "Error clearing preferred model. Check logs for details."
        )


# --- Chat Command ---
@bot.tree.command(name="chat", description="Chat with the Yuna AI.")
@discord.app_commands.describe(prompt="The question or message for the AI.")
async def chat(interaction: discord.Interaction, prompt: str):
    """The primary chat command for Yuna."""
    await interaction.response.defer(thinking=True)

    try:
        # Get author details from the interaction
        author = interaction.user
        author_details = {
            "username": author.name,
            "display_name": author.display_name,
            "server_nickname": author.nick or author.display_name,
        }

        # Convert the single prompt into the structured conversation history
        history = [
            {"role": "user", "content": prompt, "author_details": author_details}
        ]

        ai_response = await ai_manager.get_ai_response(history, interaction.guild_id)
        response_text = ai_response["content"]
        model_used = ai_response["model"]

        # Format response with model attribution
        formatted_response = f"> {prompt}\n\n{response_text}\n\n-# Model: {model_used}"
        await interaction.followup.send(formatted_response)

    except Exception as e:
        log.error(f"Error in /chat command: {e}", exc_info=True)
        await interaction.followup.send(
            "An unexpected error occurred. Please check the logs."
        )


# --- The main event listener ---
@bot.event
async def on_message(message: discord.Message):
    # Ignore messages from the bot itself
    if message.author == bot.user:
        return

    # Add debug logging (only for messages that might trigger a response)
    log.debug(
        f"Processing message from {message.author.name} in channel {message.channel.name}"
    )

    conversation_history = await gather_context(message, bot)

    if conversation_history:
        log.info(
            f"Bot will respond - found {len(conversation_history)} messages in context"
        )
        async with message.channel.typing():
            try:
                ai_response = await ai_manager.get_ai_response(
                    conversation_history, message.guild.id if message.guild else None
                )
                response_text = ai_response["content"]
                model_used = ai_response["model"]

                # Reply to the user's message to maintain the thread
                if response_text:
                    # Format response with model attribution
                    formatted_response = f"{response_text}\n\n-# Model: {model_used}"
                    await message.reply(formatted_response)
                    log.info(
                        f"Successfully replied to message from {message.author.name} using {model_used}"
                    )
                else:
                    log.warning("AI response was empty")
            except Exception as e:
                log.error(f"Error getting AI response: {e}", exc_info=True)
                await message.reply("Sorry, I encountered an error while thinking.")
    else:
        log.debug(
            f"Bot will not respond to message from {message.author.name} - no context gathered"
        )



def main():
    if not DISCORD_TOKEN:
        log.error("DISCORD_TOKEN not found in .env file.")
        return
    bot.run(DISCORD_TOKEN)


if __name__ == "__main__":
    main()
