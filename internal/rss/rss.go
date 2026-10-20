package rss

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

// Item is a simplified RSS/Atom entry used by the writer.
type Item struct {
	Title       string
	Link        string
	PublishedAt time.Time
	Source      string
}

// FetchAll fetches multiple feeds and merges items. A single failing feed is skipped.
func FetchAll(ctx context.Context, feedURLs []string, timeout time.Duration, perFeedLimit int) ([]Item, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	parser := gofeed.NewParser()
	parser.Client = &http.Client{Timeout: timeout}

	var (
		mu     sync.Mutex
		merged []Item
		wg     sync.WaitGroup
	)

	for _, u := range feedURLs {
		url := u
		wg.Add(1)
		go func() {
			defer wg.Done()
			feed, err := parser.ParseURLWithContext(url, ctx)
			if err != nil || feed == nil {
				return
			}
			res := make([]Item, 0, perFeedLimit)
			for _, it := range feed.Items {
				if it == nil || it.Link == "" || it.Title == "" {
					continue
				}
				var pub time.Time
				if it.PublishedParsed != nil {
					pub = *it.PublishedParsed
				}
				res = append(res, Item{
					Title:       it.Title,
					Link:        it.Link,
					PublishedAt: pub,
					Source:      feed.Title,
				})
				if perFeedLimit > 0 && len(res) >= perFeedLimit {
					break
				}
			}
			mu.Lock()
			merged = append(merged, res...)
			mu.Unlock()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	<-done

	mu.Lock()
	out := append([]Item(nil), merged...)
	mu.Unlock()
	if len(out) == 0 && ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, nil
}
