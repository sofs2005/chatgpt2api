package protocol

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt2api/internal/backend"
)

func TestChatCompletionCacheHitsSerialRequests(t *testing.T) {
	cache := NewChatCompletionCache(2 * time.Second)
	var calls int32
	fetch := func() (string, error) {
		atomic.AddInt32(&calls, 1)
		return "ok", nil
	}
	got1, err := cache.Do("cache-key", fetch)
	if err != nil || got1 != "ok" {
		t.Fatalf("first Do() = %q, %v; want ok, nil", got1, err)
	}
	got2, err := cache.Do("cache-key", fetch)
	if err != nil || got2 != "ok" {
		t.Fatalf("second Do() = %q, %v; want ok, nil", got2, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestChatCompletionCacheDedupesConcurrentRequests(t *testing.T) {
	cache := NewChatCompletionCache(2 * time.Second)
	var calls int32
	started := make(chan struct{})
	release := make(chan struct{})
	fetch := func() (string, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(started)
		}
		<-release
		return "ok", nil
	}

	const workers = 8
	var wg sync.WaitGroup
	results := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.Do("cache-key", fetch)
			results <- got
			errs <- err
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
	}
	for got := range results {
		if got != "ok" {
			t.Fatalf("Do() result = %q, want ok", got)
		}
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestChatCompletionCacheDoesNotStoreErrors(t *testing.T) {
	cache := NewChatCompletionCache(2 * time.Second)
	var calls int32
	wantErr := errors.New("boom")
	_, err := cache.Do("cache-key", func() (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("first Do() error = %v, want %v", err, wantErr)
	}
	got, err := cache.Do("cache-key", func() (string, error) {
		atomic.AddInt32(&calls, 1)
		return "ok", nil
	})
	if err != nil || got != "ok" {
		t.Fatalf("second Do() = %q, %v; want ok, nil", got, err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls)
	}
}

func TestChatCompletionCacheKeepsDifferentKeysSeparate(t *testing.T) {
	cache := NewChatCompletionCache(2 * time.Second)
	var calls int32
	fetch := func(value string) func() (string, error) {
		return func() (string, error) {
			atomic.AddInt32(&calls, 1)
			return value, nil
		}
	}
	got1, err := cache.Do("first", fetch("one"))
	if err != nil || got1 != "one" {
		t.Fatalf("Do(first) = %q, %v; want one, nil", got1, err)
	}
	got2, err := cache.Do("second", fetch("two"))
	if err != nil || got2 != "two" {
		t.Fatalf("Do(second) = %q, %v; want two, nil", got2, err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls)
	}
}

func TestChatCompletionsAndResponsesTextPathShareCache(t *testing.T) {
	engine, _ := newTextLeaseTestEngine(t, "token-1")
	engine.ChatCompletionCache = NewChatCompletionCache(2 * time.Second)
	var calls int32
	stubStreamTextDeltasForTokenRetry(t, func(ctx context.Context, e *Engine, client *backend.Client, request ConversationRequest) (<-chan string, <-chan error) {
		atomic.AddInt32(&calls, 1)
		deltas := make(chan string, 1)
		errs := make(chan error, 1)
		deltas <- "cached text"
		close(deltas)
		errs <- nil
		close(errs)
		return deltas, errs
	})

	chat, stream, err := engine.HandleChatCompletions(context.Background(), map[string]any{
		"model":    "gpt-5",
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
	})
	if err != nil || stream != nil {
		t.Fatalf("HandleChatCompletions() response=%#v stream=%#v err=%v", chat, stream, err)
	}
	response, stream, err := engine.HandleResponses(context.Background(), map[string]any{
		"model": "gpt-5",
		"input": "hello",
	})
	if err != nil || stream != nil {
		t.Fatalf("HandleResponses() response=%#v stream=%#v err=%v", response, stream, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("text fetch calls = %d, want 1 shared cache hit", calls)
	}
	output := response["output"].([]map[string]any)
	content := output[0]["content"].([]map[string]any)
	if content[0]["text"] != "cached text" {
		t.Fatalf("responses text = %#v, want cached text", content[0]["text"])
	}
}

func TestChatCompletionCacheDoesNotWrapImageOrStreamRoutes(t *testing.T) {
	engine, _ := newTextLeaseTestEngine(t, "token-1")
	engine.ChatCompletionCache = NewChatCompletionCache(2 * time.Second)
	var textCalls int32
	stubStreamTextDeltasForTokenRetry(t, func(ctx context.Context, e *Engine, client *backend.Client, request ConversationRequest) (<-chan string, <-chan error) {
		atomic.AddInt32(&textCalls, 1)
		deltas := make(chan string, 1)
		errs := make(chan error, 1)
		deltas <- "stream text"
		close(deltas)
		errs <- nil
		close(errs)
		return deltas, errs
	})

	for i := 0; i < 2; i++ {
		_, stream, err := engine.HandleChatCompletions(context.Background(), map[string]any{
			"model":    "gpt-5",
			"stream":   true,
			"messages": []any{map[string]any{"role": "user", "content": "hello"}},
		})
		if err != nil || stream == nil {
			t.Fatalf("stream HandleChatCompletions() stream=%#v err=%v", stream, err)
		}
		for range stream.Items {
		}
		if err := <-stream.Err; err != nil {
			t.Fatalf("stream error = %v", err)
		}
	}
	if atomic.LoadInt32(&textCalls) != 2 {
		t.Fatalf("stream text fetch calls = %d, want 2", textCalls)
	}

	imageEngine, _ := newImageLeaseTestEngine(t, "image-token")
	imageEngine.ChatCompletionCache = NewChatCompletionCache(2 * time.Second)
	var imageCalls int32
	imageEngine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		atomic.AddInt32(&imageCalls, 1)
		outputs := make(chan ImageOutput, 1)
		errs := make(chan error, 1)
		outputs <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: "image text"}
		close(outputs)
		errs <- nil
		close(errs)
		return outputs, errs
	}
	for i := 0; i < 2; i++ {
		result, stream, err := imageEngine.HandleChatCompletions(context.Background(), map[string]any{
			"model":    "gpt-image-2",
			"messages": []any{map[string]any{"role": "user", "content": "draw"}},
		})
		if err != nil || stream != nil || result == nil {
			t.Fatalf("image HandleChatCompletions() result=%#v stream=%#v err=%v", result, stream, err)
		}
	}
	if atomic.LoadInt32(&imageCalls) != 2 {
		t.Fatalf("image fetch calls = %d, want 2", imageCalls)
	}
}
