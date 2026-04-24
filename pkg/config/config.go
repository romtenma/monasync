package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Addr       string
	DBPath     string
	User       string
	Password   string
	DailyLimit int64
	DumpXML    bool
}

func Load() (Config, error) {
	// .env ファイルを読み込む（ファイルが存在しない場合は無視）
	_ = godotenv.Load()

	cfg := Config{
		Addr:       valueOrDefault("MONASYNC_ADDR", ":8081"),
		DBPath:     valueOrDefault("MONASYNC_DB_PATH", filepath.Join("data", "monasync.db")),
		User:       os.Getenv("MONASYNC_USER"),
		Password:   os.Getenv("MONASYNC_PASSWORD"),
		DailyLimit: 999,
	}

	limitValue := os.Getenv("MONASYNC_DAILY_LIMIT")
	if limitValue != "" {
		parsed, err := strconv.ParseInt(limitValue, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse sync limit: %w", err)
		}
		cfg.DailyLimit = parsed
	}

	if cfg.User == "" || cfg.Password == "" {
		return Config{}, fmt.Errorf("MONASYNC_USER and MONASYNC_PASSWORD are required")
	}

	return cfg, nil
}

func valueOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
