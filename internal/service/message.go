package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"neuro-occultist-bot/internal/llm"
	"neuro-occultist-bot/internal/rss"
	"neuro-occultist-bot/internal/storage"
	"neuro-occultist-bot/internal/textutil"
)

// RSSFetch fetches items from the configured feeds. Tests replace this.
type RSSFetch func(ctx context.Context, feedURLs []string, timeout time.Duration, perFeedLimit int) ([]rss.Item, error)

// Agent is a fixed RSS → LLM pipeline (ingest, filter, prompt, generate).
// It does not choose tools or take autonomous actions.
type Agent struct {
	store    storage.Store
	feeds    []string
	composer llm.Composer
	fetch    RSSFetch
}

// NewAgent constructs the RSS → LLM pipeline.
func NewAgent(store storage.Store, feeds []string, composer llm.Composer) *Agent {
	return &Agent{
		store:    store,
		feeds:    feeds,
		composer: composer,
		fetch:    rss.FetchAll,
	}
}

// WithRSSFetch overrides the RSS client. Used by tests.
func (a *Agent) WithRSSFetch(fn RSSFetch) *Agent {
	a.fetch = fn
	return a
}

// Digest is a batch of headlines plus the generated post.
type Digest struct {
	Texts []string
	Links []string
	Post  string
}

// BuildDigest returns the frozen pending batch if any active chat still lacks
// those links. Otherwise it fetches items not yet delivered to every active
// chat and composes one post, then stores that batch so a later RSS item cannot
// merge into a retry and resend already-delivered headlines.
func (a *Agent) BuildDigest(ctx context.Context, maxItems int) (Digest, error) {
	if a.store != nil {
		pending, err := a.store.GetPendingDigest(ctx)
		if err != nil {
			return Digest{}, fmt.Errorf("pending digest: %w", err)
		}
		if pending != nil && (pending.Post != "" || len(pending.Links) > 0) {
			done, err := a.pendingComplete(ctx, pending.Links)
			if err != nil {
				return Digest{}, err
			}
			if !done {
				return Digest{Texts: pending.Texts, Links: pending.Links, Post: pending.Post}, nil
			}
			if err := a.store.ClearPendingDigest(ctx); err != nil {
				return Digest{}, fmt.Errorf("clear pending digest: %w", err)
			}
		}
	}

	d, err := a.build(ctx, maxItems, a.FetchUnsent)
	if err != nil {
		return Digest{}, err
	}
	if len(d.Links) == 0 {
		return d, nil
	}
	if a.store != nil {
		if err := a.store.SetPendingDigest(ctx, storage.PendingDigest{
			Post:  d.Post,
			Links: append([]string(nil), d.Links...),
			Texts: append([]string(nil), d.Texts...),
		}); err != nil {
			return Digest{}, fmt.Errorf("save pending digest: %w", err)
		}
	}
	return d, nil
}

// BuildLatestDigest fetches current headlines without consulting delivery state.
// Used for personal preview commands that must not affect broadcast.
func (a *Agent) BuildLatestDigest(ctx context.Context, maxItems int) (Digest, error) {
	return a.build(ctx, maxItems, a.FetchLatest)
}

func (a *Agent) build(ctx context.Context, maxItems int, fetch func(context.Context, int) ([]string, []string, error)) (Digest, error) {
	texts, links, err := fetch(ctx, maxItems)
	if err != nil {
		return Digest{}, err
	}
	if len(texts) == 0 {
		return Digest{}, nil
	}
	post, err := a.Compose(ctx, texts)
	if err != nil {
		return Digest{}, err
	}
	return Digest{Texts: texts, Links: links, Post: post}, nil
}

// FetchLatest returns recent RSS titles/links and does not read or write delivery state.
func (a *Agent) FetchLatest(ctx context.Context, maxItems int) ([]string, []string, error) {
	return a.collect(ctx, maxItems, nil)
}

// FetchUnsent returns items that are not yet delivered to every saved chat.
func (a *Agent) FetchUnsent(ctx context.Context, maxItems int) ([]string, []string, error) {
	var chats []storage.Chat
	if a.store != nil {
		var err error
		chats, err = a.store.ListChats(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("list chats: %w", err)
		}
	}
	return a.collect(ctx, maxItems, func(link string) bool {
		return a.deliveredToAll(ctx, chats, link)
	})
}

func (a *Agent) collect(ctx context.Context, maxItems int, skip func(string) bool) ([]string, []string, error) {
	if maxItems <= 0 {
		maxItems = 6
	}
	items, err := a.fetch(ctx, a.feeds, 20*time.Second, maxItems)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch RSS: %w", err)
	}

	links := make([]string, 0, maxItems)
	texts := make([]string, 0, maxItems)

	for _, item := range items {
		if len(texts) >= maxItems {
			break
		}
		if skip != nil && skip(item.Link) {
			continue
		}
		title := textutil.SanitizeUntrusted(item.Title, 200)
		if title == "" {
			continue
		}
		links = append(links, item.Link)
		texts = append(texts, title+" — "+item.Link)
	}
	return texts, links, nil
}

func (a *Agent) pendingComplete(ctx context.Context, links []string) (bool, error) {
	if a.store == nil {
		return true, nil
	}
	if len(links) == 0 {
		return true, nil
	}
	chats, err := a.store.ListChats(ctx)
	if err != nil {
		return false, fmt.Errorf("list chats: %w", err)
	}
	if len(chats) == 0 {
		// No active recipients left: do not wait on chats that were removed.
		return true, nil
	}
	for _, link := range links {
		if !a.deliveredToAll(ctx, chats, link) {
			return false, nil
		}
	}
	return true, nil
}

func (a *Agent) deliveredToAll(ctx context.Context, chats []storage.Chat, link string) bool {
	if a.store == nil || len(chats) == 0 {
		return false
	}
	for _, ch := range chats {
		ok, err := a.store.WasDelivered(ctx, ch.ID, link)
		if err != nil {
			log.Printf("delivery lookup failed chat=%d: %v", ch.ID, err)
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

// Compose asks the LLM to write a post from headline texts.
func (a *Agent) Compose(ctx context.Context, texts []string) (string, error) {
	if a.composer == nil {
		return "", fmt.Errorf("llm client is not configured")
	}
	if len(texts) == 0 {
		return "", fmt.Errorf("no texts provided")
	}
	return a.composer.ComposePost(ctx, texts)
}

// ExtractTopics returns short titles for image prompts.
func ExtractTopics(texts []string, maxTopics int) []string {
	topics := make([]string, 0, maxTopics)
	for _, text := range texts {
		if len(topics) >= maxTopics {
			break
		}
		title, _, _ := strings.Cut(text, " — ")
		title = textutil.SanitizeUntrusted(title, 120)
		if title == "" {
			continue
		}
		topics = append(topics, title)
	}
	return topics
}
