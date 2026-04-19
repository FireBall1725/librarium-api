// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package config

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	Host                string
	Port                string
	DatabaseURL         string
	JWTSecret           string
	JWTAccessTTL        time.Duration
	JWTRefreshTTL       time.Duration
	RegistrationEnabled bool
	LogLevel            slog.Level
	CoverStoragePath      string
	EbookStoragePath      string
	AudiobookStoragePath  string
	EbookPathTemplate     string
	AudiobookPathTemplate string
	TUI                   bool
}

func Load() *Config {
	return &Config{
		Host:                 getEnv("HOST", "0.0.0.0"),
		Port:                 getEnv("PORT", "8080"),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://librarium:librarium@localhost:5432/librarium?sslmode=disable"),
		JWTSecret:            getEnv("JWT_SECRET", ""),
		JWTAccessTTL:         parseDuration(getEnv("JWT_ACCESS_TTL", "15m")),
		JWTRefreshTTL:        parseDuration(getEnv("JWT_REFRESH_TTL", "168h")),
		RegistrationEnabled:  getEnv("REGISTRATION_ENABLED", "true") != "false",
		LogLevel:             parseLogLevel(getEnv("LOG_LEVEL", "info")),
		CoverStoragePath:      getEnv("COVER_STORAGE_PATH", "./data/covers"),
		EbookStoragePath:      getEnv("EBOOK_STORAGE_PATH", "./data/media/ebooks"),
		AudiobookStoragePath:  getEnv("AUDIOBOOK_STORAGE_PATH", "./data/media/audiobooks"),
		EbookPathTemplate:     getEnv("EBOOK_PATH_TEMPLATE", "{title}"),
		AudiobookPathTemplate: getEnv("AUDIOBOOK_PATH_TEMPLATE", "{title}"),
		TUI:                  getEnv("TUI", "false") == "true",
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
