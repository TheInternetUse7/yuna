package bot

import (
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/config"
)

const imageOnlyPrompt = "Please describe this image."

// imageAttachments returns the Discord CDN URLs for a message's image
// attachments. Width and height are a fallback for endpoints that omit the
// content type while still identifying the attachment as an image.
func imageAttachments(msg *discordgo.Message) []string {
	if msg == nil {
		return nil
	}

	var urls []string
	for _, attachment := range msg.Attachments {
		if attachment == nil || attachment.URL == "" {
			continue
		}
		contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
		isImage := strings.HasPrefix(contentType, "image/")
		if contentType == "" {
			isImage = attachment.Width > 0 && attachment.Height > 0
		}
		if !isImage {
			continue
		}
		urls = append(urls, attachment.URL)
	}
	return urls
}

// imageChain narrows the model chain to models that accept image input. The
// configured model order is retained so image-capable fallbacks keep their
// operator-assigned priority.
func imageChain(chain []config.Provider) []config.Provider {
	var out []config.Provider
	for _, provider := range chain {
		models := make([]string, 0, len(provider.ImageModels))
		for _, model := range provider.Models {
			if provider.SupportsImages(model) {
				models = append(models, model)
			}
		}
		if len(models) > 0 {
			provider.Models = models
			out = append(out, provider)
		}
	}
	return out
}

// attachImages converts the newest user message into a multimodal message. The
// input is copied, so the memory layer's message slice is never mutated.
func attachImages(msgs []schemas.ChatMessage, urls []string) []schemas.ChatMessage {
	if len(urls) == 0 {
		return msgs
	}

	out := make([]schemas.ChatMessage, len(msgs))
	copy(out, msgs)

	index := -1
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == schemas.ChatMessageRoleUser {
			index = i
			break
		}
	}
	if index < 0 {
		out = append(out, schemas.ChatMessage{Role: schemas.ChatMessageRoleUser})
		index = len(out) - 1
	}

	text := imageOnlyPrompt
	blocks := make([]schemas.ChatContentBlock, 0, len(urls)+1)
	if content := out[index].Content; content != nil {
		switch {
		case content.ContentStr != nil:
			text = *content.ContentStr
		case content.ContentBlocks != nil:
			blocks = append(blocks, content.ContentBlocks...)
		}
	}
	if strings.TrimSpace(text) == "" {
		text = imageOnlyPrompt
	}
	if len(blocks) == 0 || blocks[0].Type != schemas.ChatContentBlockTypeText {
		blocks = append([]schemas.ChatContentBlock{{
			Type: schemas.ChatContentBlockTypeText,
			Text: schemas.Ptr(text),
		}}, blocks...)
	}
	for _, url := range urls {
		blocks = append(blocks, schemas.ChatContentBlock{
			Type:           schemas.ChatContentBlockTypeImage,
			ImageURLStruct: &schemas.ChatInputImage{URL: url},
		})
	}
	out[index].Content = &schemas.ChatMessageContent{ContentBlocks: blocks}
	return out
}
