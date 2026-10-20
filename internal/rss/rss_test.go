package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example Feed</title>
    <item>
      <title>First story</title>
      <link>https://example.com/1</link>
    </item>
    <item>
      <title></title>
      <link>https://example.com/empty</link>
    </item>
    <item>
      <title>Second story</title>
      <link>https://example.com/2</link>
    </item>
  </channel>
</rss>`

func TestFetchAll_parsesAndLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleFeed))
	}))
	t.Cleanup(srv.Close)

	items, err := FetchAll(context.Background(), []string{srv.URL}, 5*time.Second, 1)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %+v", items)
	}
	if items[0].Title != "First story" || items[0].Link != "https://example.com/1" {
		t.Fatalf("unexpected item: %+v", items[0])
	}
}

func TestFetchAll_skipsBadFeed(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleFeed))
	}))
	t.Cleanup(good.Close)

	items, err := FetchAll(context.Background(), []string{bad.URL, good.URL}, 5*time.Second, 5)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 valid items, got %+v", items)
	}
}
