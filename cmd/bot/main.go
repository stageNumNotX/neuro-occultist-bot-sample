package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"neuro-occultist-bot/internal/bot"
	"neuro-occultist-bot/internal/config"
	"neuro-occultist-bot/internal/handlers"
	"neuro-occultist-bot/internal/image"
	"neuro-occultist-bot/internal/llm"
	"neuro-occultist-bot/internal/service"
	"neuro-occultist-bot/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	llmClient, err := llm.New(ctx, cfg.GeminiAPIKey, cfg.GeminiModel, cfg.DigestLanguage)
	if err != nil {
		log.Fatalf("llm: %v", err)
	}
	defer func() { _ = llmClient.Close() }()

	store, err := openStore(ctx, cfg)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer func() { _ = store.Close() }()

	var images image.Generator
	if cfg.VertexAI.ProjectID != "" {
		ic, err := image.NewClient(image.Config{
			ProjectID:   cfg.VertexAI.ProjectID,
			Location:    cfg.VertexAI.Location,
			ImageModel:  cfg.VertexAI.ImageModel,
			AspectRatio: cfg.VertexAI.AspectRatio,
		})
		if err != nil {
			log.Printf("image generation disabled: %v", err)
		} else {
			images = ic
			log.Printf("image generation enabled (Vertex generateContent, model=%s)", cfg.VertexAI.ImageModel)
		}
	}

	agent := service.NewAgent(store, cfg.RSSFeeds, llmClient)

	b, err := bot.New(bot.Options{
		Token:    cfg.TelegramToken,
		Polling:  cfg.Mode == config.ModePolling,
		Store:    store,
		Agent:    agent,
		Images:   images,
		AdminIDs: cfg.AdminUserIDs,
	})
	if err != nil {
		log.Fatalf("telegram: %v", err)
	}

	mux := http.NewServeMux()
	handlers.New(cfg, b, agent, images).Register(mux)

	if err := serve(ctx, ":"+cfg.Port, cfg.Mode, b, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func openStore(ctx context.Context, cfg config.AppConfig) (storage.Store, error) {
	if cfg.ProjectID == "" {
		log.Printf("FIRESTORE_PROJECT_ID unset; using in-memory store")
		return storage.NewMemory(), nil
	}
	fs, err := storage.NewFirestore(ctx, cfg.ProjectID, cfg.FirestoreDB)
	if err != nil {
		log.Printf("firestore unavailable (%v); using in-memory store", err)
		return storage.NewMemory(), nil
	}
	log.Printf("firestore connected (database=%s)", cfg.FirestoreDB)
	return fs, nil
}
