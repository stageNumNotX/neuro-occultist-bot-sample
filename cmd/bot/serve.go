package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"neuro-occultist-bot/internal/config"
)

// telegramCtl is the subset of the Telegram adapter needed for process lifetime.
type telegramCtl interface {
	Start()
	Stop()
}

func serve(ctx context.Context, addr, mode string, tg telegramCtl, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("http listening on %s (mode=%s)", srv.Addr, mode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	poller := mode == config.ModePolling
	if poller && tg != nil {
		go tg.Start()
		log.Printf("telegram long polling started")
	} else {
		log.Printf("telegram webhook path: /webhook")
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		log.Printf("http server: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	if poller && tg != nil {
		tg.Stop()
	}
	return nil
}
