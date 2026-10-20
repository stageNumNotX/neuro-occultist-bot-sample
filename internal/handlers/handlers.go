package handlers

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"time"

	"neuro-occultist-bot/internal/config"
	"neuro-occultist-bot/internal/image"
	"neuro-occultist-bot/internal/service"
)

// BotService is the Telegram adapter used by HTTP routes.
type BotService interface {
	HandleWebhook(w http.ResponseWriter, r *http.Request)
	Broadcast(ctx context.Context, post string, img []byte, links []string) error
}

// Handlers serves health, webhook, and broadcast routes.
type Handlers struct {
	cfg    config.AppConfig
	bot    BotService
	agent  *service.Agent
	images image.Generator
}

// New constructs HTTP handlers.
func New(cfg config.AppConfig, bot BotService, agent *service.Agent, images image.Generator) *Handlers {
	return &Handlers{cfg: cfg, bot: bot, agent: agent, images: images}
}

// Register attaches routes to mux.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/webhook", h.webhook)
	mux.HandleFunc("/broadcast", h.broadcast)
	mux.HandleFunc("/healthz", h.health)
	mux.HandleFunc("/", h.root)
}

func (h *Handlers) webhook(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Mode == config.ModePolling {
		http.NotFound(w, r)
		return
	}
	if h.cfg.WebhookSecret == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.cfg.WebhookSecret)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	h.bot.HandleWebhook(w, r)
}

func (h *Handlers) broadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.cfg.BroadcastSecret == "" {
		http.NotFound(w, r)
		return
	}
	got := firstNonEmpty(r.Header.Get("X-Broadcast-Secret"), r.Header.Get("X-Cron-Token"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.cfg.BroadcastSecret)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if h.agent == nil {
		writeText(w, http.StatusServiceUnavailable, "pipeline unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	digest, err := h.agent.BuildDigest(ctx, 6)
	if err != nil {
		log.Printf("broadcast digest: %v", err)
		writeText(w, http.StatusInternalServerError, "broadcast failed")
		return
	}
	if len(digest.Texts) == 0 {
		writeText(w, http.StatusNoContent, "no new items")
		return
	}

	var img []byte
	if h.images != nil {
		img, err = h.images.Generate(ctx, image.BuildNewsPrompt(service.ExtractTopics(digest.Texts, 3)))
		if err != nil {
			log.Printf("broadcast image: %v", err)
			img = nil
		}
	}

	if err := h.bot.Broadcast(ctx, digest.Post, img, digest.Links); err != nil {
		log.Printf("broadcast send: %v", err)
		writeText(w, http.StatusInternalServerError, "broadcast failed")
		return
	}
	writeText(w, http.StatusOK, "broadcasted")
}

func (h *Handlers) health(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

func (h *Handlers) root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeText(w, http.StatusOK, "Neuro-Occultist Bot")
}

func writeText(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
