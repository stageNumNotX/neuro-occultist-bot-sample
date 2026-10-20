package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"neuro-occultist-bot/internal/rss"
	"neuro-occultist-bot/internal/storage"
)

type fakeComposer struct {
	post      string
	err       error
	lastItems []string
}

func (f *fakeComposer) ComposePost(_ context.Context, items []string) (string, error) {
	f.lastItems = append([]string(nil), items...)
	return f.post, f.err
}

func TestFetchUnsent_skipsFullyDelivered(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1, Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := mem.SaveChat(ctx, storage.Chat{ID: 2, Title: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDelivered(ctx, 1, "https://example.com/old"); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDelivered(ctx, 2, "https://example.com/old"); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(mem, []string{"http://feeds.example/rss"}, &fakeComposer{post: "ok"})
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{
			{Title: "Old", Link: "https://example.com/old"},
			{Title: "New", Link: "https://example.com/new"},
		}, nil
	}

	texts, links, err := a.FetchUnsent(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 || links[0] != "https://example.com/new" {
		t.Fatalf("texts=%v links=%v", texts, links)
	}
}

func TestFetchUnsent_keepsPartialDelivery(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := mem.SaveChat(ctx, storage.Chat{ID: 2}); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDelivered(ctx, 1, "https://example.com/x"); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(mem, nil, &fakeComposer{})
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "X", Link: "https://example.com/x"}}, nil
	}

	_, links, err := a.FetchUnsent(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("partial delivery must still be offered to remaining chats, links=%v", links)
	}
}

func TestFetchLatest_ignoresDeliveryState(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDelivered(ctx, 1, "https://example.com/old"); err != nil {
		t.Fatal(err)
	}

	a := NewAgent(mem, nil, &fakeComposer{})
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Old", Link: "https://example.com/old"}}, nil
	}

	_, links, err := a.FetchLatest(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("personal fetch must not skip delivered links, links=%v", links)
	}

	unsent, _, err := a.FetchUnsent(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsent) != 0 {
		t.Fatalf("broadcast fetch should skip fully delivered, got %v", unsent)
	}
}

func TestFetchUnsent_noChatsStillReturnsItems(t *testing.T) {
	ctx := context.Background()
	a := NewAgent(storage.NewMemory(), nil, &fakeComposer{})
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "H", Link: "https://example.com/h"}}, nil
	}
	_, links, err := a.FetchUnsent(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("empty chat list must not drop items, links=%v", links)
	}
}

func TestBuildDigest_usesComposer(t *testing.T) {
	ctx := context.Background()
	fc := &fakeComposer{post: "mystical post"}
	a := NewAgent(storage.NewMemory(), nil, fc)
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Headline", Link: "https://example.com/h"}}, nil
	}

	d, err := a.BuildDigest(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if d.Post != "mystical post" {
		t.Fatalf("post=%q", d.Post)
	}
	if len(fc.lastItems) != 1 {
		t.Fatalf("composer items=%v", fc.lastItems)
	}
}

func TestBuildDigest_composerError(t *testing.T) {
	a := NewAgent(storage.NewMemory(), nil, &fakeComposer{err: errors.New("boom")})
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "H", Link: "https://example.com/h"}}, nil
	}
	if _, err := a.BuildDigest(context.Background(), 3); err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractTopics(t *testing.T) {
	topics := ExtractTopics([]string{
		"Go 1.23 released — https://example.com/1",
		"Other news — https://example.com/2",
	}, 1)
	if len(topics) != 1 || topics[0] != "Go 1.23 released" {
		t.Fatalf("topics=%v", topics)
	}
}
