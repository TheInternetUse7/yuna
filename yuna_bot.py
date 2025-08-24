# yuna_bot.py
import os
import discord
from discord.ext import commands
from dotenv import load_dotenv
import database
import ai_manager

# Load environment variables
load_dotenv()
DISCORD_TOKEN = os.getenv("DISCORD_TOKEN")
GROQ_API_KEY = os.getenv("GROQ_API_KEY")

# Bot setup
intents = discord.Intents.default()
intents.message_content = True
intents.members = True  # <-- ADD THIS LINE
bot = commands.Bot(command_prefix="/", intents=intents)


@bot.event
async def on_ready():
    """Event that fires when the bot is connected and ready."""
    print(f"{bot.user} has connected to Discord!")
    print("Initializing database...")
    database.initialize_database()
    # After initializing, you should manually add your Groq key
    if GROQ_API_KEY:
        database.add_api_key("groq", GROQ_API_KEY)
        print("Groq API key loaded from .env and stored in database.")
    else:
        print(
            "Warning: GROQ_API_KEY not found in .env file. The /chat command will not work."
        )

    print("Syncing slash commands...")
    await bot.tree.sync()
    print("Yuna is ready.")


@bot.tree.command(name="chat", description="Chat with the Yuna AI.")
async def chat(interaction: discord.Interaction, *, prompt: str):
    """The primary chat command for Yuna."""
    # Acknowledge the command immediately to prevent "interaction failed"
    await interaction.response.defer(thinking=True)

    try:
        # Convert the single prompt into the structured conversation history
        history = [{"role": "user", "content": prompt}]

        # Get the response from the AI manager
        response_text = await ai_manager.get_ai_response(history)

        # Send the response
        await interaction.followup.send(f"> {prompt}\n\n{response_text}")

    except Exception as e:
        print(f"Error in /chat command: {e}")
        await interaction.followup.send(
            "An unexpected error occurred. Please check the logs."
        )


@bot.tree.command(
    name="set_ai_channel", description="Designates a channel for the AI to respond in."
)
@commands.has_permissions(administrator=True)
async def set_ai_channel(interaction: discord.Interaction, channel: str | None = None):
    """Command to set a channel as an AI channel. Accepts channel mention, id, or name; defaults to current channel."""
    # Resolve channel string to a TextChannel object
    resolved = None
    if channel is None:
        resolved = interaction.channel
    else:
        import re

        m = re.match(r"<#(\d+)>", channel)
        if m:
            cid = int(m.group(1))
            resolved = bot.get_channel(cid)
        elif channel.isdigit():
            resolved = bot.get_channel(int(channel))
        else:
            # Strip leading # if present
            name = channel.lstrip("#")
            guild = interaction.guild
            if guild:
                resolved = discord.utils.get(guild.text_channels, name=name)

    if not resolved:
        await interaction.response.send_message(
            "Could not resolve channel. Use a channel mention, id, or name.",
            ephemeral=True,
        )
        return

    database.add_ai_channel(resolved.id, interaction.guild.id)
    await interaction.response.send_message(
        f"Channel {resolved.mention} has been set as an AI channel."
    )


@bot.tree.command(
    name="remove_ai_channel", description="Removes a channel from the AI response list."
)
@commands.has_permissions(administrator=True)
async def remove_ai_channel(
    interaction: discord.Interaction, channel: str | None = None
):
    """Command to remove a channel from the AI-enabled list. Accepts mention, id, or name; defaults to current channel."""
    resolved = None
    if channel is None:
        resolved = interaction.channel
    else:
        import re

        m = re.match(r"<#(\d+)>", channel)
        if m:
            cid = int(m.group(1))
            resolved = bot.get_channel(cid)
        elif channel.isdigit():
            resolved = bot.get_channel(int(channel))
        else:
            name = channel.lstrip("#")
            guild = interaction.guild
            if guild:
                resolved = discord.utils.get(guild.text_channels, name=name)

    if not resolved:
        await interaction.response.send_message(
            "Could not resolve channel. Use a channel mention, id, or name.",
            ephemeral=True,
        )
        return

    database.remove_ai_channel(resolved.id)
    await interaction.response.send_message(
        f"Channel {resolved.mention} is no longer an AI channel."
    )


async def gather_context(message: discord.Message):
    """Gathers context from a message, walking up reply chains or channel history."""
    history = []
    current_message = message

    # If it's a reply, walk up the chain
    if message.reference and isinstance(message.reference.resolved, discord.Message):
        while current_message:
            author = current_message.author
            role = "assistant" if author == bot.user else "user"
            history.append(
                {
                    "role": role,
                    "content": current_message.content,
                    "author_details": {
                        "username": str(author),
                        "display_name": author.display_name,
                        "server_nickname": author.nick,
                    },
                }
            )
            if (
                len(history) >= 15
                or not current_message.reference
                or not isinstance(current_message.reference.resolved, discord.Message)
            ):
                break
            current_message = current_message.reference.resolved
        history.reverse()  # Chronological order
    # If it's in an AI channel, get the last few messages
    elif database.is_ai_channel(message.channel.id):
        async for msg in message.channel.history(limit=15):
            author = msg.author
            role = "assistant" if author == bot.user else "user"
            history.append(
                {
                    "role": role,
                    "content": msg.content,
                    "author_details": {
                        "username": str(author),
                        "display_name": author.display_name,
                        "server_nickname": author.nick,
                    },
                }
            )
        history.reverse()
    # Otherwise, it's a direct mention
    else:
        author = message.author
        # Correctly handle both <@BOT_ID> and <@!BOT_ID> formats
        content = (
            message.content.replace(f"<@{bot.user.id}>", "")
            .replace(f"<@!{bot.user.id}>", "")
            .strip()
        )
        history.append(
            {
                "role": "user",
                "content": content,
                "author_details": {
                    "username": str(author),
                    "display_name": author.display_name,
                    "server_nickname": author.nick,
                },
            }
        )
    return history


@bot.event
async def on_message(message: discord.Message):
    """Handles incoming messages to trigger the AI."""
    if message.author == bot.user:
        return

    # Check if the bot was mentioned, is being replied to, or is in an AI channel
    is_reply = (
        message.reference
        and message.reference.resolved
        and message.reference.resolved.author == bot.user
    )
    is_mention = bot.user.mentioned_in(message)
    is_in_ai_channel = database.is_ai_channel(message.channel.id)

    if not (is_reply or is_mention or is_in_ai_channel):
        return

    async with message.channel.typing():
        try:
            # Gather the conversational context
            context_history = await gather_context(message)

            # Get the response from the AI manager
            response_text = await ai_manager.get_ai_response(context_history)

            # Send the response
            await message.reply(response_text)

        except Exception as e:
            print(f"Error in on_message: {e}")
            await message.channel.send(
                "An unexpected error occurred. Please check the logs."
            )


def main():
    if not DISCORD_TOKEN:
        print("Error: DISCORD_TOKEN not found in .env file.")
        return
    bot.run(DISCORD_TOKEN)


if __name__ == "__main__":
    main()
