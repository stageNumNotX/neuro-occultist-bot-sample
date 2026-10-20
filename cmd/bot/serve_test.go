package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"neuro-occultist-bot/internal/bot"
	"neuro-occultist-bot/internal/config"
)

type stubTelegram struct {
	start atomic.Int32
	stop  atomic.Int32
	gate  chan struct{}
}

func newStubTelegram() *stubTelegram {
	return &stubTelegram{gate: make(chan struct{})}
}

func (s *stubTelegram) Start() {
	s.start.Add(1)
	<-s.gate
}

func (s *stubTelegram) Stop() {
	s.stop.Add(1)
	select {
	case <-s.gate:
	default:
		close(s.gate)
	}
}

func TestServe_webhookCancelDoesNotStopPoller(t *testing.T) {
	tg := newStubTelegram()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, "127.0.0.1:0", config.ModeWebhook, tg, mux)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook shutdown did not finish within 2s")
	}
	if tg.start.Load() != 0 || tg.stop.Load() != 0 {
		t.Fatalf("webhook mode must not start/stop poller start=%d stop=%d", tg.start.Load(), tg.stop.Load())
	}
}

func TestServe_pollingStopsPoller(t *testing.T) {
	tg := newStubTelegram()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, "127.0.0.1:0", config.ModePolling, tg, mux)
	}()

	deadline := time.Now().Add(time.Second)
	for tg.start.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tg.start.Load() == 0 {
		t.Fatal("poller was not started")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("polling shutdown did not finish within 2s")
	}
	if tg.stop.Load() == 0 {
		t.Fatal("polling mode must stop the poller")
	}
}

func TestServe_webhookRealBotStopDoesNotHang(t *testing.T) {
	b, err := bot.New(bot.Options{Token: "test", Offline: true, Polling: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, "127.0.0.1:0", config.ModeWebhook, b, mux)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SIGTERM-equivalent cancel hung; Bot.Stop was likely called without Start")
	}
}
