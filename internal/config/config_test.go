package config

import (
	"testing"
)

func TestLoad_pollingMinimal(t *testing.T) {
	t.Setenv("BOT_MODE", "polling")
	t.Setenv("TELEGRAM_TOKEN", "test-token")
	t.Setenv("GEMINI_API_KEY", "test-gemini")
	t.Setenv("PORT", "")
	t.Setenv("WEBHOOK_SECRET", "")
	t.Setenv("FIRESTORE_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("RSS_FEEDS", "")
	t.Setenv("FEEDS", "")
	t.Setenv("ADMIN_USER_IDS", "42, 99")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode != ModePolling {
		t.Fatalf("mode=%q", cfg.Mode)
	}
	if cfg.Port != "8080" {
		t.Fatalf("port=%q", cfg.Port)
	}
	if len(cfg.RSSFeeds) < 1 {
		t.Fatal("expected default RSS feeds")
	}
	if _, ok := cfg.AdminUserIDs[42]; !ok {
		t.Fatal("admin 42 missing")
	}
	if _, ok := cfg.AdminUserIDs[99]; !ok {
		t.Fatal("admin 99 missing")
	}
	if cfg.DigestLanguage != "Russian" {
		t.Fatalf("default digest language=%q", cfg.DigestLanguage)
	}
}

func TestLoad_digestLanguageAliases(t *testing.T) {
	t.Setenv("BOT_MODE", "polling")
	t.Setenv("TELEGRAM_TOKEN", "t")
	t.Setenv("GEMINI_API_KEY", "g")
	t.Setenv("DIGEST_LANGUAGE", "en")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DigestLanguage != "English" {
		t.Fatalf("language=%q", cfg.DigestLanguage)
	}
}

func TestLoad_webhookRequiresSecret(t *testing.T) {
	t.Setenv("BOT_MODE", "webhook")
	t.Setenv("TELEGRAM_TOKEN", "test-token")
	t.Setenv("GEMINI_API_KEY", "test-gemini")
	t.Setenv("WEBHOOK_SECRET", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when webhook secret is missing")
	}
}

func TestLoad_missingToken(t *testing.T) {
	t.Setenv("BOT_MODE", "polling")
	t.Setenv("TELEGRAM_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("GEMINI_API_KEY", "test-gemini")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when telegram token is missing")
	}
}

func TestLoad_customFeeds(t *testing.T) {
	t.Setenv("BOT_MODE", "polling")
	t.Setenv("TELEGRAM_TOKEN", "t")
	t.Setenv("GEMINI_API_KEY", "g")
	t.Setenv("RSS_FEEDS", " https://a.example/rss , https://b.example/feed ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.RSSFeeds) != 2 || cfg.RSSFeeds[0] != "https://a.example/rss" {
		t.Fatalf("feeds=%v", cfg.RSSFeeds)
	}
}
