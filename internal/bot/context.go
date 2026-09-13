package bot

import (
	"github.com/bwmarrin/discordgo"

	"github.com/TheInternetUse7/yuna/internal/store"
)

// replyChainLimit bounds how far Yuna walks a reply chain looking for one of
// her own messages, so a long thread cannot cost unbounded lookups.
const replyChainLimit = 15

// decision is what Yuna does with one message, and why. The reason exists so
// the log can name the trigger that fired. Messages that trigger nothing are
// dropped before they reach the log, so their reason is used only by tests.
type decision struct {
	store   bool
	respond bool
	reason  string
}

// classify applies the trigger and persistence policies in one place, so the
// two can never drift apart: anything Yuna answers is also stored. It is pure,
// so it is tested without a gateway.
func classify(msg *discordgo.Message, botID string, isAIChannel, replyTargetsBot bool) decision {
	switch {
	case msg == nil || msg.Author == nil:
		return decision{reason: "the message has no author"}
	case msg.Author.ID == botID:
		// Yuna's own replies are always kept; never answered again.
		return decision{store: true, reason: "yuna's own message"}
	case msg.Author.Bot:
		// Ignoring every bot is what stops bot-to-bot loops.
		return decision{reason: "the author is a bot"}
	case msg.GuildID == "": // a direct message, always answered
		return decision{store: true, respond: true, reason: "direct message"}
	case isAIChannel:
		return decision{store: true, respond: true, reason: "ai channel"}
	case mentionsUser(msg, botID):
		return decision{store: true, respond: true, reason: "mentions yuna"}
	case replyTargetsBot:
		return decision{store: true, respond: true, reason: "reply reaches yuna"}
	default:
		return decision{reason: "no mention, not an ai channel, not a reply"}
	}
}

// shouldRespond decides whether Yuna answers a message.
func shouldRespond(msg *discordgo.Message, botID string, isAIChannel, replyTargetsBot bool) bool {
	return classify(msg, botID, isAIChannel, replyTargetsBot).respond
}

// shouldStore implements the persistence policy.
//
// This is a privacy decision, not a size one: Yuna keeps a message only when it
// has a reason to remember it. Messages that merely happen to be in a channel
// Yuna can see are never written to disk, never summarised, and never shown to
// a model.
func shouldStore(msg *discordgo.Message, botID string, isAIChannel, replyTargetsBot bool) bool {
	return classify(msg, botID, isAIChannel, replyTargetsBot).store
}

func mentionsUser(msg *discordgo.Message, userID string) bool {
	for _, u := range msg.Mentions {
		if u != nil && u.ID == userID {
			return true
		}
	}
	return false
}

func isBotAuthored(msg *discordgo.Message, botID string) bool {
	return msg != nil && msg.Author != nil && msg.Author.ID == botID
}

func referencedID(msg *discordgo.Message) string {
	if msg == nil || msg.MessageReference == nil {
		return ""
	}
	return msg.MessageReference.MessageID
}

// replyChainReachesBot reports whether the message is a reply, at any depth, to
// something Yuna wrote.
//
// Stored messages are consulted first, which covers every message Yuna authored
// because her own replies are always persisted. A link that is not stored is
// fetched from Discord, because the bot deliberately does not store most
// traffic and a chain may pass through messages it never kept.
func (b *Bot) replyChainReachesBot(channelID string, msg *discordgo.Message) bool {
	nextID := referencedID(msg)
	for hop := 0; hop < replyChainLimit && nextID != ""; hop++ {
		stored, err := b.store.MessageBySnowflake(channelID, nextID)
		if err != nil {
			b.log.Warnf("reply chain lookup for %s: %v", nextID, err)
			return false
		}
		if stored != nil {
			if stored.Role == store.RoleAssistant {
				return true
			}
			nextID = stored.ReplyToMessageID
			continue
		}

		fetched, err := b.fetcher.ChannelMessage(channelID, nextID)
		if err != nil {
			b.log.Debugf("reply chain fetch for %s: %v", nextID, err)
			return false
		}
		if isBotAuthored(fetched, b.appID) {
			return true
		}
		nextID = referencedID(fetched)
	}
	return false
}
