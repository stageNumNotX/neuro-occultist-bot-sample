package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tb "gopkg.in/telebot.v3"

	"neuro-occultist-bot/internal/rss"
	"neuro-occultist-bot/internal/service"
	"neuro-occultist-bot/internal/storage"
)

type fakeComposer struct {
	post string
}

func (f fakeComposer) ComposePost(_ context.Context, _ []string) (string, error) {
	return f.post, nil
}

type fakeImages struct {
	data []byte
	err  error
}

func (f fakeImages) Generate(_ context.Context, _ string) ([]byte, error) {
	return f.data, f.err
}

type recordedSend struct {
	Method string
	ChatID string
	Text   string
}

type telegramAPI struct {
	mu      sync.Mutex
	sends   []recordedSend
	failFor map[string]bool
	failNth map[int]bool
	n       int
}

func (a *telegramAPI) roundTrip(req *http.Request) (*http.Response, error) {
	method := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
	chatID, text := parseSend(req)
	a.mu.Lock()
	a.n++
	n := a.n
	fail := a.failFor[chatID] || a.failNth[n]
	if method == "sendMessage" || method == "sendPhoto" {
		a.sends = append(a.sends, recordedSend{Method: method, ChatID: chatID, Text: text})
	}
	a.mu.Unlock()

	if fail {
		return jsonResponse(http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked"}`)
	}
	body := `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":1,"type":"channel"}}}`
	if method == "sendPhoto" {
		body = `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":1,"type":"private"},"photo":[{"file_id":"fid","file_unique_id":"uid","width":1,"height":1}]}}`
	}
	return jsonResponse(http.StatusOK, body)
}

func (a *telegramAPI) snapshot() []recordedSend {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]recordedSend, len(a.sends))
	copy(out, a.sends)
	return out
}

func parseSend(req *http.Request) (chatID, text string) {
	ct := req.Header.Get("Content-Type")
	media, _, _ := mime.ParseMediaType(ct)
	if strings.HasPrefix(media, "multipart/") {
		if err := req.ParseMultipartForm(1 << 20); err == nil && req.MultipartForm != nil {
			if v := req.MultipartForm.Value["chat_id"]; len(v) > 0 {
				chatID = v[0]
			}
			if v := req.MultipartForm.Value["caption"]; len(v) > 0 {
				text = v[0]
			}
			if v := req.MultipartForm.Value["text"]; len(v) > 0 {
				text = v[0]
			}
		}
		return chatID, text
	}
	body, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	var payload map[string]string
	_ = json.Unmarshal(body, &payload)
	return payload["chat_id"], payload["text"]
}

func jsonResponse(code int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func testBot(t *testing.T, store storage.Store, agent *service.Agent, images fakeImages, api *telegramAPI, polling bool) *Bot {
	t.Helper()
	opts := Options{
		Token:       "test-token",
		Offline:     true,
		Synchronous: true,
		Polling:     polling,
		Store:       store,
		Agent:       agent,
		HTTP:        &http.Client{Transport: roundTripFunc(api.roundTrip)},
	}
	if images.data != nil || images.err != nil {
		opts.Images = images
	}
	b, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func saveChats(t *testing.T, store storage.Store, ids ...int64) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		if err := store.SaveChat(ctx, storage.Chat{ID: id, Title: "c", Type: "channel"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBroadcast_allFailDoesNotMark(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	saveChats(t, mem, 1, 2)
	api := &telegramAPI{failFor: map[string]bool{"1": true, "2": true}}
	b := testBot(t, mem, nil, fakeImages{}, api, false)

	err := b.Broadcast(ctx, "hello", nil, []string{"https://example.com/a"})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err=%v", err)
	}
	for _, id := range []int64{1, 2} {
		was, err := mem.WasDelivered(ctx, id, "https://example.com/a")
		if err != nil || was {
			t.Fatalf("chat %d marked=%v err=%v", id, was, err)
		}
	}
	if n := len(api.snapshot()); n != 2 {
		t.Fatalf("sends=%d", n)
	}
}

func TestBroadcast_partialThenRetrySkipsSuccess(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	saveChats(t, mem, 1, 2)
	api := &telegramAPI{failFor: map[string]bool{"2": true}}
	b := testBot(t, mem, nil, fakeImages{}, api, false)
	links := []string{"https://example.com/a"}

	err := b.Broadcast(ctx, "hello", nil, links)
	if !errors.Is(err, ErrPartial) {
		t.Fatalf("err=%v", err)
	}
	was1, _ := mem.WasDelivered(ctx, 1, links[0])
	was2, _ := mem.WasDelivered(ctx, 2, links[0])
	if !was1 || was2 {
		t.Fatalf("was1=%v was2=%v", was1, was2)
	}

	api.failFor = nil
	if err := b.Broadcast(ctx, "hello", nil, links); err != nil {
		t.Fatalf("retry: %v", err)
	}
	sends := api.snapshot()
	var retry []string
	for i, s := range sends {
		if i >= 2 {
			retry = append(retry, s.ChatID)
		}
	}
	if len(retry) != 1 || retry[0] != "2" {
		t.Fatalf("retry sends to %v, want only chat 2", retry)
	}
	was2, _ = mem.WasDelivered(ctx, 2, links[0])
	if !was2 {
		t.Fatal("chat 2 should be marked after retry")
	}
}

func TestBroadcast_emptyChats(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	api := &telegramAPI{}
	b := testBot(t, mem, nil, fakeImages{}, api, false)
	err := b.Broadcast(ctx, "hello", nil, []string{"https://example.com/a"})
	if !errors.Is(err, ErrNoChats) {
		t.Fatalf("err=%v", err)
	}
	if n := len(api.snapshot()); n != 0 {
		t.Fatalf("unexpected sends=%d", n)
	}
}

func TestRSSSummary_doesNotMarkBroadcastState(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	saveChats(t, mem, 10)
	link := "https://example.com/news"
	agent := service.NewAgent(mem, nil, fakeComposer{post: "personal-digest"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "News", Link: link}}, nil
	})
	api := &telegramAPI{}
	b := testBot(t, mem, agent, fakeImages{}, api, false)

	b.ProcessUpdate(tb.Update{Message: &tb.Message{
		ID:       1,
		Sender:   &tb.User{ID: 99, FirstName: "A"},
		Chat:     &tb.Chat{ID: 99, Type: tb.ChatPrivate},
		Unixtime: 1,
		Text:     "/rss_summary",
	}})

	was, err := mem.WasDelivered(ctx, 10, link)
	if err != nil || was {
		t.Fatalf("personal view marked broadcast delivery was=%v err=%v", was, err)
	}
	texts, _, err := agent.FetchUnsent(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 {
		t.Fatalf("broadcast still needs this item, texts=%v", texts)
	}

	found := false
	for _, s := range api.snapshot() {
		if strings.Contains(s.Text, "personal-digest") {
			found = true
		}
	}
	if !found {
		t.Fatalf("digest was not sent: %+v", api.snapshot())
	}
}

func TestRSSSummary_sendErrorDoesNotMark(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	saveChats(t, mem, 10)
	link := "https://example.com/news"
	agent := service.NewAgent(mem, nil, fakeComposer{post: "personal-digest"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "News", Link: link}}, nil
	})
	api := &telegramAPI{failNth: map[int]bool{2: true}}
	b := testBot(t, mem, agent, fakeImages{}, api, false)

	b.ProcessUpdate(tb.Update{Message: &tb.Message{
		ID:       1,
		Sender:   &tb.User{ID: 99, FirstName: "A"},
		Chat:     &tb.Chat{ID: 99, Type: tb.ChatPrivate},
		Unixtime: 1,
		Text:     "/rss_summary",
	}})

	was, err := mem.WasDelivered(ctx, 10, link)
	if err != nil || was {
		t.Fatalf("failed personal send marked delivery was=%v err=%v", was, err)
	}
	if n := len(api.snapshot()); n != 2 {
		t.Fatalf("sends=%d want 2 (status + digest)", n)
	}
}

func TestNewsVision_doesNotMarkBroadcastState(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	saveChats(t, mem, 10)
	link := "https://example.com/news"
	agent := service.NewAgent(mem, nil, fakeComposer{post: "x"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "News", Link: link}}, nil
	})
	api := &telegramAPI{}
	b := testBot(t, mem, agent, fakeImages{data: []byte("PNG")}, api, false)

	b.ProcessUpdate(tb.Update{Message: &tb.Message{
		ID:       1,
		Sender:   &tb.User{ID: 99, FirstName: "A"},
		Chat:     &tb.Chat{ID: 99, Type: tb.ChatPrivate},
		Unixtime: 1,
		Text:     "/news_vision",
	}})

	was, err := mem.WasDelivered(ctx, 10, link)
	if err != nil || was {
		t.Fatalf("news_vision marked delivery was=%v err=%v", was, err)
	}
}

func TestStop_withoutStartReturns(t *testing.T) {
	b := testBot(t, storage.NewMemory(), nil, fakeImages{}, &telegramAPI{}, false)
	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung on a bot that never started polling")
	}
}

func TestBroadcast_sendUsesTelegramHTTP(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	saveChats(t, mem, 42)
	api := &telegramAPI{}
	b := testBot(t, mem, nil, fakeImages{}, api, false)
	if err := b.Broadcast(ctx, "channel post", nil, []string{"https://example.com/a"}); err != nil {
		t.Fatal(err)
	}
	sends := api.snapshot()
	if len(sends) != 1 || sends[0].ChatID != "42" || sends[0].Text != "channel post" {
		t.Fatalf("sends=%+v", sends)
	}
	was, _ := mem.WasDelivered(ctx, 42, "https://example.com/a")
	if !was {
		t.Fatal("successful send must mark delivered")
	}
}
