package protocol

import (
	"strings"
	"testing"
)

func TestResponseContextStoreScopesByUser(t *testing.T) {
	store := NewResponseContextStore(10)
	store.SetScoped("linuxdo:123", "resp_1", ResponseContext{
		Messages: []map[string]any{{"role": "user", "content": "alice prompt"}},
	})

	if _, ok := store.GetScoped("linuxdo:456", "resp_1"); ok {
		t.Fatal("different user scope should not read response context")
	}
	if _, ok := store.Get("resp_1"); ok {
		t.Fatal("unscoped lookup should not read scoped response context")
	}
	ctx, ok := store.GetScoped("linuxdo:123", "resp_1")
	if !ok {
		t.Fatal("same user scope should read response context")
	}
	if len(ctx.Messages) != 1 || ctx.Messages[0]["content"] != "alice prompt" {
		t.Fatalf("scoped context = %#v", ctx)
	}
}

func TestResponseContextPreservesLongMessagesAndAllImages(t *testing.T) {
	longText := strings.Repeat("长内容", 800) + "完整尾巴"
	images := []string{"img1", "img2", "img3", "img4", "img5"}

	ctx := trimResponseContext(ResponseContext{
		Messages: []map[string]any{{"role": "assistant", "content": longText}},
		Images:   images,
	})

	if len(ctx.Messages) != 1 || ctx.Messages[0]["content"] != longText {
		t.Fatalf("message context was truncated: %#v", ctx.Messages)
	}
	if len(ctx.Images) != len(images) || ctx.Images[0] != "img1" || ctx.Images[len(ctx.Images)-1] != "img5" {
		t.Fatalf("images were truncated: %#v", ctx.Images)
	}
}

func TestResponseContextPreservesEmbeddedImageDataText(t *testing.T) {
	text := "data:image/png;base64," + strings.Repeat("A", 900) + "完整尾巴"

	ctx := trimResponseContext(ResponseContext{
		Messages: []map[string]any{{"role": "user", "content": text}},
	})

	if len(ctx.Messages) != 1 || ctx.Messages[0]["content"] != text {
		t.Fatalf("embedded image data text was replaced: %#v", ctx.Messages)
	}
}

func TestBuildImageContextPromptKeepsEntireHistory(t *testing.T) {
	messages := []map[string]any{{"role": "system", "content": "最早历史标记"}}
	for i := 0; i < 20; i++ {
		messages = append(messages, map[string]any{"role": "assistant", "content": "历史内容"})
	}
	messages = append(messages, map[string]any{"role": "user", "content": "当前请求"})

	prompt := BuildImageContextPrompt(messages, LatestUserPrompt(messages), "1:1", "high")

	if !strings.Contains(prompt, "最早历史标记") {
		t.Fatalf("image context prompt dropped early history: %s", prompt)
	}
	if !strings.Contains(prompt, "当前请求:\n当前请求") {
		t.Fatalf("image context prompt missing current request: %s", prompt)
	}
}
