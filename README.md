# Yuna AI Discord Bot

<div align="center">

![Discord](https://img.shields.io/badge/Discord-Bot-7289da?style=for-the-badge&logo=discord&logoColor=white)
![Python](https://img.shields.io/badge/Python-3.10+-3776ab?style=for-the-badge&logo=python&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)

**A resilient, multi-provider AI Discord bot with intelligent fallback and enterprise-grade features**

[Features](#-features) • [Quick Start](#-quick-start) • [Deployment](#-deployment-guide) • [Configuration](#-configuration) • [Commands](#-commands)

</div>

---

## 🚀 Features

### **Core Capabilities**
- **🤖 Multi-Provider AI Support** - AI providers with intelligent fallback (Gemini, OpenRouter, Cerebras, Groq, Cohere, Together AI)
- **⚡ Smart Rate Limit Handling** - Automatic cooldowns and provider switching on rate limits
- **🎯 Model Attribution** - Every response shows which AI model generated it
- **🔧 Admin Model Override** - Administrators can manually select preferred models per server
- **💬 Multiple Interaction Methods** - Slash commands, mentions, replies, and dedicated AI channels

### **Interaction Methods**
1. **Slash Commands** - `/chat [prompt]` for direct AI interaction
2. **Direct Mentions** - `@Yuna hello there!` for natural conversation
3. **Reply Chains** - Reply to bot messages to continue conversations
4. **AI Channels** - Designated channels where the bot responds to every message

### **Enterprise Features**
- **🛡️ Resilient Architecture** - Handles provider failures gracefully
- **📊 Comprehensive Logging** - Rotating logs with Unicode support and detailed debugging
- **⚙️ Per-Server Configuration** - Each Discord server can have its own settings
- **🔒 Admin-Only Commands** - Secure management interface for administrators
- **💾 Persistent Storage** - SQLite database for settings, cooldowns, and preferences

---

## ⚡ Quick Start

### **Prerequisites**
- Python 3.10 or higher
- Discord Bot Token ([Create one here](https://discord.com/developers/applications))
- At least one AI provider API key (see [Provider Setup](#provider-api-keys))

### **Installation**

1. **Clone the repository:**
```bash
git clone https://github.com/TheInternetUse7/yuna
cd yuna
```

2. **Create virtual environment:**
```bash
# Windows
python -m venv .venv
.\.venv\Scripts\Activate.ps1

# Linux/macOS
python -m venv .venv
source .venv/bin/activate
```

3. **Install dependencies:**
```bash
pip install -r requirements.txt
```

4. **Configure environment:**
```bash
# Copy example environment file
cp .env.example .env

# Edit .env with your tokens (see Configuration section)
```

5. **Run the bot:**
```bash
python yuna_bot.py
```

### **First Time Setup**
1. Invite the bot to your Discord server with appropriate permissions
2. Use `/set_ai_channel` in a channel to enable AI responses
3. Test with `/chat Hello!` or mention the bot
4. Check `/provider_status` to see which AI providers are available

---

## 🚀 Deployment Guide

### **Local Development**

Perfect for testing and development:

```bash
# 1. Setup environment
python -m venv .venv
source .venv/bin/activate  # or .\.venv\Scripts\Activate.ps1 on Windows
pip install -r requirements.txt

# 2. Configure
cp .env.example .env
# Edit .env with your tokens

# 3. Run
python yuna_bot.py
```

### **Production Deployment**

#### **Option 1: VPS/Cloud Server (Recommended)**

**System Requirements:**
- Ubuntu 20.04+ / CentOS 8+ / Debian 11+
- 1GB RAM minimum (2GB recommended)
- 10GB disk space
- Python 3.10+

**Step-by-step deployment:**

```bash
# 1. Update system
sudo apt update && sudo apt upgrade -y

# 2. Install Python and dependencies
sudo apt install python3.10 python3.10-venv python3-pip git -y

# 3. Create bot user (security best practice)
sudo useradd -m -s /bin/bash yuna-bot
sudo su - yuna-bot

# 4. Clone and setup
git clone https://github.com/TheInternetUse7/yuna
cd yuna
python3.10 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt

# 5. Configure environment
cp .env.example .env
nano .env  # Add your tokens

# 6. Test run
python yuna_bot.py
```

**Create systemd service for auto-start:**

```bash
# Exit bot user
exit

# Create service file
sudo nano /etc/systemd/system/yuna-bot.service
```

Add this content:
```ini
[Unit]
Description=Yuna Discord AI Bot
After=network.target

[Service]
Type=simple
User=yuna-bot
WorkingDirectory=/home/yuna-bot/yuna
Environment=PATH=/home/yuna-bot/yuna/.venv/bin
ExecStart=/home/yuna-bot/yuna/.venv/bin/python yuna_bot.py
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

**Enable and start service:**
```bash
sudo systemctl daemon-reload
sudo systemctl enable yuna-bot
sudo systemctl start yuna-bot

# Check status
sudo systemctl status yuna-bot

# View logs
sudo journalctl -u yuna-bot -f
```

#### **Option 2: Docker Deployment**

**Create Dockerfile:**
```dockerfile
FROM python:3.10-slim

WORKDIR /app

# Install dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application
COPY . .

# Create non-root user
RUN useradd -m -u 1000 yuna && chown -R yuna:yuna /app
USER yuna

# Run bot
CMD ["python", "yuna_bot.py"]
```

**Create docker-compose.yml:**
```yaml
version: '3.8'

services:
  yuna-bot:
    build: .
    container_name: yuna
    restart: unless-stopped
    environment:
      - DISCORD_TOKEN=${DISCORD_TOKEN}
      - GEMINI_API_KEY=${GEMINI_API_KEY}
      - GROQ_API_KEY=${GROQ_API_KEY}
      # Add other API keys as needed
    volumes:
      - ./data:/app/data  # Persist database and logs
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
```

**Deploy with Docker:**
```bash
# Build and run
docker-compose up -d

# View logs
docker-compose logs -f

# Update deployment
git pull
docker-compose build
docker-compose up -d
```

#### **Option 3: Cloud Platform Deployment**

**Heroku:**
```bash
# Install Heroku CLI
# Create Procfile
echo "worker: python yuna_bot.py" > Procfile

# Deploy
heroku create your-bot-name
heroku config:set DISCORD_TOKEN=your_token
heroku config:set GEMINI_API_KEY=your_key
git push heroku main
heroku ps:scale worker=1
```

**Railway/Render:**
- Connect your GitHub repository
- Set environment variables in dashboard
- Deploy automatically on git push

### **Production Monitoring**

**Log Management:**
```bash
# View live logs
tail -f yuna.log

# Rotate logs manually
logrotate -f /etc/logrotate.d/yuna-bot
```

**Health Monitoring:**
```bash
# Check bot status
systemctl status yuna-bot

# Monitor resource usage
htop
df -h
```

**Backup Strategy:**
```bash
# Backup database and logs
tar -czf yuna-backup-$(date +%Y%m%d).tar.gz yuna.db yuna.log*

# Automated daily backup
echo "0 2 * * * cd /home/yuna-bot/yuna && tar -czf backups/yuna-backup-\$(date +\%Y\%m\%d).tar.gz yuna.db yuna.log*" | crontab -
```

---

## ⚙️ Configuration

### **Environment Variables**

Create a `.env` file in the project root:

```bash
# Required: Discord Bot Token
DISCORD_TOKEN=your_discord_bot_token_here

# Optional: AI Provider API Keys (add as many as you want)
GEMINI_API_KEY=your_google_ai_studio_key
GROQ_API_KEY=your_groq_api_key
OPENROUTER_API_KEY=your_openrouter_key
CEREBRAS_API_KEY=your_cerebras_key
COHERE_API_KEY=your_cohere_key
TOGETHER_AI_API_KEY=your_together_ai_key

# Optional: Logging Configuration
LOG_LEVEL=INFO
LOG_FILE_MAX_SIZE=5242880  # 5MB in bytes
LOG_BACKUP_COUNT=3
```

### **Provider API Keys**

The bot works with any combination of these providers. **You only need at least one API key** to get started:

#### **🥇 Recommended Providers (Free Tiers Available)**

**Google Gemini (Primary)**
- **Get Key**: [Google AI Studio](https://aistudio.google.com/app/apikey)
- **Free Tier**: 15 requests/minute, 1 million tokens/day
- **Environment Variable**: `GEMINI_API_KEY`

**Groq (Fast Inference)**
- **Get Key**: [Groq Console](https://console.groq.com/keys)
- **Free Tier**: 30 requests/minute, 14,400 tokens/day
- **Environment Variable**: `GROQ_API_KEY`

**Cerebras (High Performance)**
- **Get Key**: [Cerebras Inference](https://inference.cerebras.ai/)
- **Free Tier**: Available with registration
- **Environment Variable**: `CEREBRAS_API_KEY`

**OpenRouter**
- **Get Key**: [OpenRouter](https://openrouter.ai/keys)
- **Pricing**: Pay-per-use, access to premium models
- **Environment Variable**: `OPENROUTER_API_KEY`

**Cohere**
- **Get Key**: [Cohere Dashboard](https://dashboard.cohere.ai/api-keys)
- **Free Trial**: Available, then pay-per-use
- **Environment Variable**: `COHERE_API_KEY`

**Together AI (Open Source Models)**
- **Get Key**: [Together AI](https://api.together.xyz/settings/api-keys)
- **Pricing**: Competitive rates for open source models
- **Environment Variable**: `TOGETHER_AI_API_KEY`

### **Bot Configuration**

**Configurable Constants** (in `yuna_bot.py`):
```python
# Message Context Limits
AI_CHANNEL_MESSAGE_LIMIT = 15  # Messages to fetch in AI channels
REPLY_CHAIN_LIMIT = 15         # Max reply chain depth

# Logging Configuration
LOG_FILE_MAX_SIZE = 5 * 1024 * 1024  # 5MB max log file size
LOG_BACKUP_COUNT = 3                  # Number of backup log files
```

**Provider Configuration** (in `ai_manager.py`):
```python
# Cooldown Timers
DEFAULT_COOLDOWN_SECONDS = 300      # 5 minutes for rate limits
GENERAL_ERROR_COOLDOWN_SECONDS = 60 # 1 minute for other errors
```

### **Discord Bot Setup**

1. **Create Discord Application:**
   - Go to [Discord Developer Portal](https://discord.com/developers/applications)
   - Click "New Application" and give it a name
   - Go to "Bot" section and click "Add Bot"
   - Copy the bot token for your `.env` file

2. **Set Bot Permissions:**
   Required permissions for full functionality:
   ```
   ✅ Send Messages
   ✅ Use Slash Commands
   ✅ Read Message History
   ✅ Add Reactions
   ✅ Mention Everyone (for @mentions)
   ✅ Use External Emojis
   ```

3. **Invite Bot to Server:**
   - Go to "OAuth2" > "URL Generator"
   - Select "bot" and "applications.commands" scopes
   - Select the permissions above
   - Use generated URL to invite bot

4. **Initial Server Setup:**
   ```
   /set_ai_channel          # Make current channel an AI channel
   /provider_status         # Check which providers are available
   /chat Hello world!       # Test the bot
   ```

---

## 🎮 Commands

### **User Commands**

| Command | Description | Example |
|---------|-------------|---------|
| `/chat [prompt]` | Direct AI conversation | `/chat Explain quantum computing` |
| `@Yuna [message]` | Mention bot for natural chat | `@Yuna what's the weather like?` |
| Reply to bot | Continue conversation thread | Reply to any bot message |

### **Admin Commands**

*Requires Administrator permission*

| Command | Description | Example |
|---------|-------------|---------|
| `/set_ai_channel` | Make current channel AI-enabled | `/set_ai_channel` |
| `/remove_ai_channel` | Disable AI in current channel | `/remove_ai_channel` |
| `/provider_status` | Show AI provider availability | `/provider_status` |
| `/set_preferred_model [provider] [model]` | Override automatic model selection | `/set_preferred_model gemini` |
| `/clear_preferred_model` | Return to automatic selection | `/clear_preferred_model` |

### **Usage Examples**

**Basic Conversation:**
```
User: /chat What is machine learning?
Yuna: Machine learning is a subset of artificial intelligence...

-# Model: gemini/gemini-2.5-flash
```

**AI Channel (responds to every message):**
```
User: How do I deploy a Python app?
Yuna: There are several ways to deploy a Python application...

-# Model: openrouter/openai/gpt-oss-120b
```

**Admin Model Override:**
```
Admin: /set_preferred_model groq
Yuna: ✅ Set preferred model for this server: groq
      Model: groq/llama4-maverick-17b-128e-instruct
```

**Provider Status Check:**
```
Admin: /provider_status
Yuna: 🤖 Provider Status: 4/6 available

      ✅ Openrouter - openrouter/openai/gpt-oss-120b (Score: 57.9)
      ✅ Cerebras - cerebras/qwen-3-235b-a22b (Score: 45.3)
      ⏳ Gemini - Cooldown (2m 30s remaining)
      ✅ Groq - groq/llama4-maverick-17b-128e-instruct (Score: 35.8)
```

---

## 🏗️ Architecture

### **System Overview**

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Discord API   │◄──►│   yuna_bot.py    │◄──►│   ai_manager.py │
│                 │    │                  │    │                 │
│ • Slash Commands│    │ • Event Handling │    │ • Provider Logic│
│ • Message Events│    │ • Context Gather │    │ • Fallback      │
│ • Interactions  │    │ • Response Format│    │ • Rate Limiting │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                                │                        │
                                ▼                        ▼
                       ┌──────────────────┐    ┌─────────────────┐
                       │   database.py    │    │   LiteLLM       │
                       │                  │    │                 │
                       │ • SQLite Storage │    │ • Multi-Provider│
                       │ • Cooldown Mgmt  │    │ • Unified API   │
                       │ • Preferences    │    │ • Error Handling│
                       └──────────────────┘    └─────────────────┘
```

### **Key Components**

**`yuna_bot.py`** - Discord Interface
- Event handling (messages, interactions)
- Context gathering from conversations
- Response formatting with model attribution
- Admin command processing

**`ai_manager.py`** - AI Orchestration
- Multi-provider fallback logic
- Rate limit and cooldown management
- Provider priority and selection
- Response generation coordination

**`database.py`** - Data Persistence
- SQLite database management
- AI channel configuration
- Rate limit cooldown tracking
- Preferred model storage

**Provider Integration** - LiteLLM
- Unified interface to AI providers
- Automatic error handling and retries
- Consistent response formatting
- Built-in rate limit detection

### **Data Flow**

1. **User Input** → Discord message/command received
2. **Context Gathering** → Collect conversation history
3. **Provider Selection** → Choose best available AI provider
4. **AI Generation** → Generate response with fallback
5. **Response Formatting** → Add model attribution
6. **Discord Output** → Send formatted response

### **File Structure**

```
yuna/
├── yuna_bot.py              # Main Discord bot application
├── ai_manager.py            # AI provider orchestration
├── database.py              # SQLite database interface
├── requirements.txt         # Python dependencies
├── .env.example            # Environment template
├── README.md               # This documentation
├── yuna.db                 # SQLite database (created on first run)
└── yuna.log                # Application logs (rotating)
```

---

## 🔧 Troubleshooting

### **Common Issues**

**Bot doesn't respond to messages:**
```bash
# Check bot permissions
- Ensure bot has "Send Messages" permission
- Verify bot can read message history
- Check if channel is set as AI channel: /set_ai_channel

# Check logs
tail -f yuna.log
```

**"All providers failed" error:**
```bash
# Verify API keys are set
grep -E "(GEMINI|GROQ|OPENROUTER)" .env

# Check provider status
# Use /provider_status command in Discord

# Wait for cooldowns to expire
# Providers may be temporarily rate limited
```

**Bot crashes on startup:**
```bash
# Check Python version
python --version  # Should be 3.10+

# Verify dependencies
pip install -r requirements.txt

# Check Discord token
echo $DISCORD_TOKEN  # Should not be empty
```

**Database errors:**
```bash
# Reset database (WARNING: loses all settings)
rm yuna.db
python yuna_bot.py  # Will recreate database

# Check database permissions
ls -la yuna.db
```

### **Debug Mode**

Enable detailed logging:
```python
# In yuna_bot.py, change logging level
logging.basicConfig(level=logging.DEBUG, ...)
```

### **Performance Issues**

**High memory usage:**
- Reduce `AI_CHANNEL_MESSAGE_LIMIT` and `REPLY_CHAIN_LIMIT`
- Enable log rotation with smaller file sizes
- Monitor with `htop` or `ps aux | grep python`

**Slow responses:**
- Check provider response times in logs
- Consider using faster providers (Groq, Cerebras)
- Reduce context window size

### **Getting Help**

1. **Check logs first**: `tail -f yuna.log`
2. **Test with minimal config**: Use only one API key
3. **Verify Discord permissions**: Bot needs proper server permissions
4. **Check provider status**: Use `/provider_status` command
5. **Review environment**: Ensure all required variables are set

---

## 📊 Monitoring & Maintenance

### **Log Analysis**

**View real-time logs:**
```bash
tail -f yuna.log | grep -E "(ERROR|WARNING|SUCCESS)"
```

**Common log patterns:**
```bash
# Successful responses
grep "✅ Success with" yuna.log

# Rate limit hits
grep "⚠️ Rate limit hit" yuna.log

# Provider failures
grep "🚨 All available providers failed" yuna.log
```

### **Health Checks**

**System health script:**
```bash
#!/bin/bash
# health_check.sh

# Check if bot process is running
if pgrep -f "yuna_bot.py" > /dev/null; then
    echo "✅ Bot is running"
else
    echo "❌ Bot is not running"
    exit 1
fi

# Check log for recent activity (last 5 minutes)
if find yuna.log -mmin -5 | grep -q yuna.log; then
    echo "✅ Recent activity detected"
else
    echo "⚠️ No recent activity"
fi

# Check database accessibility
if sqlite3 yuna.db "SELECT COUNT(*) FROM ai_channels;" > /dev/null 2>&1; then
    echo "✅ Database accessible"
else
    echo "❌ Database error"
    exit 1
fi
```

### **Automated Maintenance**

**Log cleanup cron job:**
```bash
# Add to crontab: crontab -e
0 3 * * 0 find /path/to/yuna -name "yuna.log.*" -mtime +30 -delete
```

**Database optimization:**
```bash
# Weekly database cleanup
0 2 * * 0 sqlite3 /path/to/yuna.db "VACUUM;"
```

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgments

- **[LiteLLM](https://github.com/BerriAI/litellm)** - Unified AI provider interface
- **[Discord.py](https://github.com/Rapptz/discord.py)** - Discord API wrapper
- **[Artificial Analysis](https://artificialanalysis.ai/)** - AI model intelligence rankings

---

<div align="center">

**Made with ❤️**

[⭐ Star this repo](https://github.com/TheInternetUse7/yuna) • [🐛 Report Bug](https://github.com/TheInternetUse7/yuna/issues) • [💡 Request Feature](https://github.com/TheInternetUse7/yuna/issues)

</div>
