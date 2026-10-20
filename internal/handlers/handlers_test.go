package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"neuro-occultist-bot/internal/bot"
	"neuro-occultist-bot/internal/config"
	"neuro-occultist-bot/internal/rss"
	"neuro-occultist-bot/internal/service"
	"neuro-occultist-bot/internal/storage"
)

type fakeBot struct {
	webhookCalls int
	broadcasts   int
	lastPost     string
	lastImgLen   int
	lastLinks    []string
	store        storage.Store
	err          error
}

func (f *fakeBot) HandleWebhook(w http.ResponseWriter, _ *http.Request) {
	f.webhookCalls++
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (f *fakeBot) Broadcast(ctx context.Context, post string, img []byte, links []string) error {
	if f.err != nil {
		return f.err
	}
	f.broadcasts++
	f.lastPost = post
	f.lastImgLen = len(img)
	f.lastLinks = append([]string(nil), links...)
	if f.store != nil {
		chats, err := f.store.ListChats(ctx)
		if err != nil {
			return err
		}
		if len(chats) == 0 {
			return bot.ErrNoChats
		}
		for _, ch := range chats {
			for _, link := range links {
				if err := f.store.MarkDelivered(ctx, ch.ID, link); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type fakeComposer struct {
	post string
	err  error
}

func (f fakeComposer) ComposePost(_ context.Context, _ []string) (string, error) {
	return f.post, f.err
}

type fakeImages struct {
	data []byte
	err  error
}

func (f fakeImages) Generate(_ context.Context, _ string) ([]byte, error) {
	return f.data, f.err
}

func testMux(cfg config.AppConfig, b BotService, agent *service.Agent, img *fakeImages) *http.ServeMux {
	h := New(cfg, b, agent, nil)
	if img != nil {
		h.images = img
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func postWebhook(mux http.Handler, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{}`))
	if secret != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	mux := testMux(config.AppConfig{}, &fakeBot{}, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestWebhook_pollingModeNotAccepted(t *testing.T) {
	fb := &fakeBot{}
	mux := testMux(config.AppConfig{Mode: config.ModePolling}, fb, nil, nil)
	rec := postWebhook(mux, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("polling without secret code=%d", rec.Code)
	}
	rec = postWebhook(mux, "anything")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("polling with header code=%d", rec.Code)
	}
	if fb.webhookCalls != 0 {
		t.Fatal("webhook must not be dispatched in polling mode")
	}
}

func TestWebhook_missingSecretRejected(t *testing.T) {
	fb := &fakeBot{}
	mux := testMux(config.AppConfig{Mode: config.ModeWebhook}, fb, nil, nil)
	rec := postWebhook(mux, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rec.Code)
	}
	if fb.webhookCalls != 0 {
		t.Fatal("empty secret must not skip authentication")
	}
}

func TestWebhook_wrongSecretRejected(t *testing.T) {
	fb := &fakeBot{}
	mux := testMux(config.AppConfig{Mode: config.ModeWebhook, WebhookSecret: "s3cret"}, fb, nil, nil)
	rec := postWebhook(mux, "nope")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rec.Code)
	}
	if fb.webhookCalls != 0 {
		t.Fatal("bot should not be called with a wrong secret")
	}
}

func TestWebhook_validSecretAccepted(t *testing.T) {
	fb := &fakeBot{}
	mux := testMux(config.AppConfig{Mode: config.ModeWebhook, WebhookSecret: "s3cret"}, fb, nil, nil)
	rec := postWebhook(mux, "s3cret")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if fb.webhookCalls != 1 {
		t.Fatalf("calls=%d", fb.webhookCalls)
	}
}

func TestBroadcast_disabledWithoutSecret(t *testing.T) {
	mux := testMux(config.AppConfig{}, &fakeBot{}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestBroadcast_unauthorized(t *testing.T) {
	mux := testMux(config.AppConfig{BroadcastSecret: "abc"}, &fakeBot{}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "wrong")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestBroadcast_successWithMockedLLM(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1, Title: "chan"}); err != nil {
		t.Fatal(err)
	}
	fb := &fakeBot{store: mem}
	agent := service.NewAgent(mem, nil, fakeComposer{post: "hello aether"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Go 1.23", Link: "https://example.com/go"}}, nil
	})
	img := &fakeImages{data: []byte("PNG")}
	mux := testMux(config.AppConfig{BroadcastSecret: "abc"}, fb, agent, img)

	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "abc")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if fb.broadcasts != 1 || fb.lastPost != "hello aether" {
		t.Fatalf("broadcasts=%d post=%q", fb.broadcasts, fb.lastPost)
	}
	if fb.lastImgLen != 3 {
		t.Fatalf("img len=%d", fb.lastImgLen)
	}

	req = httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "abc")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		body, _ := io.ReadAll(rec.Body)
		t.Fatalf("second broadcast code=%d body=%s", rec.Code, body)
	}
}

func TestBroadcast_imageErrorStillSends(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1, Title: "chan"}); err != nil {
		t.Fatal(err)
	}
	fb := &fakeBot{store: mem}
	agent := service.NewAgent(mem, nil, fakeComposer{post: "hello aether"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Go 1.23", Link: "https://example.com/go"}}, nil
	})
	img := &fakeImages{err: errors.New("vertex generateContent 404")}
	mux := testMux(config.AppConfig{BroadcastSecret: "abc"}, fb, agent, img)

	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "abc")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if fb.broadcasts != 1 || fb.lastImgLen != 0 {
		t.Fatalf("broadcasts=%d imgLen=%d", fb.broadcasts, fb.lastImgLen)
	}
	was, err := mem.WasDelivered(ctx, 1, "https://example.com/go")
	if err != nil || !was {
		t.Fatalf("successful text send after image failure should mark, was=%v err=%v", was, err)
	}
}

func TestBroadcast_DoesNotMarkWhenComposeFails(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1}); err != nil {
		t.Fatal(err)
	}
	fb := &fakeBot{store: mem}
	agent := service.NewAgent(mem, nil, fakeComposer{err: errors.New("gemini 404")})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Go 1.23", Link: "https://example.com/go"}}, nil
	})
	mux := testMux(config.AppConfig{BroadcastSecret: "abc"}, fb, agent, nil)

	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "abc")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d", rec.Code)
	}
	if fb.broadcasts != 0 {
		t.Fatalf("Broadcast must not run after LLM failure, calls=%d", fb.broadcasts)
	}
	was, err := mem.WasDelivered(ctx, 1, "https://example.com/go")
	if err != nil || was {
		t.Fatalf("LLM failure must not mark posted, was=%v err=%v", was, err)
	}
}

func TestBroadcast_failedSendDoesNotMark(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1}); err != nil {
		t.Fatal(err)
	}
	fb := &fakeBot{err: bot.ErrAllFailed}
	agent := service.NewAgent(mem, nil, fakeComposer{post: "post"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Go 1.23", Link: "https://example.com/go"}}, nil
	})
	mux := testMux(config.AppConfig{BroadcastSecret: "abc"}, fb, agent, nil)

	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "abc")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d", rec.Code)
	}

	was, err := mem.WasDelivered(ctx, 1, "https://example.com/go")
	if err != nil || was {
		t.Fatalf("failed broadcast must not mark delivered, was=%v err=%v", was, err)
	}
}

func TestBroadcast_stateSaveErrorIsNotSuccess(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	if err := mem.SaveChat(ctx, storage.Chat{ID: 1}); err != nil {
		t.Fatal(err)
	}
	fb := &fakeBot{err: bot.ErrStateSave}
	agent := service.NewAgent(mem, nil, fakeComposer{post: "post"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Go 1.23", Link: "https://example.com/go"}}, nil
	})
	mux := testMux(config.AppConfig{BroadcastSecret: "abc"}, fb, agent, nil)

	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "abc")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestBroadcast_emptyChats(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemory()
	fb := &fakeBot{err: bot.ErrNoChats}
	agent := service.NewAgent(mem, nil, fakeComposer{post: "post"})
	agent.WithRSSFetch(func(_ context.Context, _ []string, _ time.Duration, _ int) ([]rss.Item, error) {
		return []rss.Item{{Title: "Go 1.23", Link: "https://example.com/go"}}, nil
	})
	mux := testMux(config.AppConfig{BroadcastSecret: "abc"}, fb, agent, nil)

	req := httptest.NewRequest(http.MethodPost, "/broadcast", nil)
	req.Header.Set("X-Broadcast-Secret", "abc")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d", rec.Code)
	}
	was, err := mem.WasDelivered(ctx, 1, "https://example.com/go")
	if err != nil || was {
		t.Fatalf("empty chat list must not mark items, was=%v err=%v", was, err)
	}
}

func TestRoot_unknownPath404(t *testing.T) {
	mux := testMux(config.AppConfig{}, &fakeBot{}, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d", rec.Code)
	}
}
