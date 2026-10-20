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
	calls     int
	lastItems []string
}

func (f *fakeComposer) ComposePost(_ context.Context, items []string) (string, error) {
	f.calls++
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
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1, Title: "A"}); err != nil {
		t.Fatal(err)
	}
	link := "https://example.com/h"
	a := NewAgent(mem, nil, &fakeComposer{err: errors.New("boom")})
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "H", Link: link}}, nil
	}
	if _, err := a.BuildDigest(ctx, 3); err == nil {
		t.Fatal("expected error")
	}
	was, err := mem.WasDelivered(ctx, 1, link)
	if err != nil || was {
		t.Fatalf("LLM failure must not mark RSS items posted, was=%v err=%v", was, err)
	}
	pending, err := mem.GetPendingDigest(ctx)
	if err != nil || pending != nil {
		t.Fatalf("LLM failure must not freeze a digest, pending=%v err=%v", pending, err)
	}
}

func TestBuildDigest_emptyRSSDoesNotCallComposer(t *testing.T) {
	fc := &fakeComposer{post: "should-not-run"}
	a := NewAgent(storage.NewMemory(), nil, fc)
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return nil, nil
	}
	d, err := a.BuildDigest(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if d.Post != "" || fc.calls != 0 {
		t.Fatalf("empty RSS must not compose, digest=%+v calls=%d", d, fc.calls)
	}
}

func TestBuildDigest_reusesPendingWhenRSSGrows(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1, Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := mem.SaveChat(ctx, storage.Chat{ID: 2, Title: "B"}); err != nil {
		t.Fatal(err)
	}

	fc := &fakeComposer{post: "post-X"}
	items := []rss.Item{{Title: "X", Link: "https://example.com/x"}}
	a := NewAgent(mem, nil, fc)
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return items, nil
	}

	d1, err := a.BuildDigest(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if d1.Post != "post-X" || len(d1.Links) != 1 || d1.Links[0] != "https://example.com/x" {
		t.Fatalf("first digest=%+v", d1)
	}
	if err := mem.MarkDelivered(ctx, 1, "https://example.com/x"); err != nil {
		t.Fatal(err)
	}

	if fc.calls != 1 {
		t.Fatalf("first compose calls=%d", fc.calls)
	}
	fc.post = "post-XY"
	items = []rss.Item{
		{Title: "X", Link: "https://example.com/x"},
		{Title: "Y", Link: "https://example.com/y"},
	}

	d2, err := a.BuildDigest(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Post != "post-X" {
		t.Fatalf("retry must reuse frozen post, got %q", d2.Post)
	}
	if len(d2.Links) != 1 || d2.Links[0] != "https://example.com/x" {
		t.Fatalf("retry must not merge Y into frozen batch, links=%v", d2.Links)
	}
	if fc.calls != 1 {
		t.Fatalf("composer must not run again while pending digest is open, calls=%d", fc.calls)
	}

	if err := mem.MarkDelivered(ctx, 2, "https://example.com/x"); err != nil {
		t.Fatal(err)
	}
	fc.post = "post-Y"
	d3, err := a.BuildDigest(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if d3.Post != "post-Y" || len(d3.Links) != 1 || d3.Links[0] != "https://example.com/y" {
		t.Fatalf("after batch complete, Y should be a new digest, got %+v", d3)
	}
}

func TestBuildDigest_removeChatUnblocksPending(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1, Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := mem.SaveChat(ctx, storage.Chat{ID: 2, Title: "B"}); err != nil {
		t.Fatal(err)
	}
	fc := &fakeComposer{post: "post-X"}
	items := []rss.Item{{Title: "X", Link: "https://example.com/x"}}
	a := NewAgent(mem, nil, fc)
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return items, nil
	}

	if _, err := a.BuildDigest(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDelivered(ctx, 1, "https://example.com/x"); err != nil {
		t.Fatal(err)
	}
	if err := mem.RemoveChat(ctx, 2); err != nil {
		t.Fatal(err)
	}

	wasB, err := mem.WasDelivered(ctx, 2, "https://example.com/x")
	if err != nil || wasB {
		t.Fatalf("removed chat must not get a fake delivery, was=%v err=%v", wasB, err)
	}

	items = []rss.Item{
		{Title: "X", Link: "https://example.com/x"},
		{Title: "Y", Link: "https://example.com/y"},
	}
	fc.post = "post-Y"
	d, err := a.BuildDigest(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if d.Post != "post-Y" || len(d.Links) != 1 || d.Links[0] != "https://example.com/y" {
		t.Fatalf("removing B must complete X for remaining chats, got %+v", d)
	}
}

func TestBuildLatestDigest_ignoresPending(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SetPendingDigest(ctx, storage.PendingDigest{
		Post:  "frozen-X",
		Links: []string{"https://example.com/x"},
		Texts: []string{"X — https://example.com/x"},
	}); err != nil {
		t.Fatal(err)
	}
	fc := &fakeComposer{post: "latest-Y"}
	a := NewAgent(mem, nil, fc)
	a.fetch = func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Y", Link: "https://example.com/y"}}, nil
	}

	d, err := a.BuildLatestDigest(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if d.Post != "latest-Y" || d.Links[0] != "https://example.com/y" {
		t.Fatalf("personal digest must ignore pending batch, got %+v", d)
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
