package util

import (
	"log/slog"
	"os"
)

type Logger = *slog.Logger

func NewLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
