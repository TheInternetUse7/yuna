package bot

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/config"
)

func TestImageAttachmentsAcceptOnlyImages(t *testing.T) {
	msg := &discordgo.Message{Attachments: []*discordgo.MessageAttachment{
		{ID: "png", URL: "https://cdn.example/png.png", ContentType: "image/png"},
		nil,
		{ID: "jpeg", URL: "https://cdn.example/jpeg.jpg", ContentType: "image/jpeg"},
		{ID: "text", URL: "https://cdn.example/file.txt", ContentType: "text/plain"},
		{ID: "video", URL: "https://cdn.example/video.mp4", ContentType: "video/mp4", Width: 12, Height: 8},
		{ID: "missing-type", URL: "https://cdn.example/photo.webp", Width: 12, Height: 8},
	}}

	got := imageAttachments(msg)
	want := "https://cdn.example/png.png|https://cdn.example/jpeg.jpg|https://cdn.example/photo.webp"
	if strings.Join(got, "|") != want {
		t.Fatalf("image URLs = %v, want %v", got, want)
	}
}

func TestImageChainKeepsOnlyImageCapableModels(t *testing.T) {
	chain := []config.Provider{
		{Name: "text", Models: []string{"text-a"}},
		{
			Name:        "mixed",
			Models:      []string{"text-b", "vision-b", "vision-c"},
			ImageModels: []string{"vision-b", "vision-c"},
		},
		{Name: "vision", Models: []string{"vision-a"}, ImageModels: []string{"vision-a"}},
	}

	got := imageChain(chain)
	if len(got) != 2 {
		t.Fatalf("image chain has %d providers, want 2: %+v", len(got), got)
	}
	if got[0].Name != "mixed" || strings.Join(got[0].Models, ",") != "vision-b,vision-c" {
		t.Fatalf("first image provider = %s with models %v", got[0].Name, got[0].Models)
	}
	if got[1].Name != "vision" || strings.Join(got[1].Models, ",") != "vision-a" {
		t.Fatalf("second image provider = %s with models %v", got[1].Name, got[1].Models)
	}
}

func TestAttachImagesBuildsMultimodalUserMessage(t *testing.T) {
	msgs := []schemas.ChatMessage{
		{Role: schemas.ChatMessageRoleSystem, Content: &schemas.ChatMessageContent{
			ContentStr: schemas.Ptr("system"),
		}},
		{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{
			ContentStr: schemas.Ptr("User 'alice' says: what is this?"),
		}},
		{Role: schemas.ChatMessageRoleAssistant, Content: &schemas.ChatMessageContent{
			ContentStr: schemas.Ptr("an earlier reply"),
		}},
		{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{
			ContentStr: schemas.Ptr("User 'alice' says: this one"),
		}},
	}

	got := attachImages(msgs, []string{"https://cdn.example/a.png", "https://cdn.example/b.png"})
	if len(got) != len(msgs) {
		t.Fatalf("message count = %d, want %d", len(got), len(msgs))
	}
	last := got[len(got)-1]
	if last.Role != schemas.ChatMessageRoleUser || last.Content.ContentStr != nil {
		t.Fatalf("last message = %+v, want a user message using content blocks", last)
	}
	blocks := last.Content.ContentBlocks
	if len(blocks) != 3 {
		t.Fatalf("block count = %d, want text plus two images", len(blocks))
	}
	if blocks[0].Type != schemas.ChatContentBlockTypeText || blocks[0].Text == nil ||
		*blocks[0].Text != "User 'alice' says: this one" {
		t.Fatalf("text block = %+v", blocks[0])
	}
	for i, want := range []string{"https://cdn.example/a.png", "https://cdn.example/b.png"} {
		block := blocks[i+1]
		if block.Type != schemas.ChatContentBlockTypeImage || block.ImageURLStruct == nil || block.ImageURLStruct.URL != want {
			t.Fatalf("image block %d = %+v, want URL %s", i, block, want)
		}
	}
}

func TestAttachImagesUsesPromptForImageOnlyMessage(t *testing.T) {
	msgs := []schemas.ChatMessage{{
		Role:    schemas.ChatMessageRoleUser,
		Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("")},
	}}

	got := attachImages(msgs, []string{"https://cdn.example/a.png"})
	blocks := got[0].Content.ContentBlocks
	if len(blocks) != 2 || blocks[0].Text == nil || *blocks[0].Text != imageOnlyPrompt {
		t.Fatalf("blocks = %+v, want a prompt followed by the image", blocks)
	}
}
