package protocol

import "testing"

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
