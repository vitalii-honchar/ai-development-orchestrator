package logger

import (
	"log/slog"
	"os"
)

func ConfigureLogger(production bool) {
	level := slog.LevelDebug
	if production {
		level = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	slog.SetDefault(slog.New(handler))
}
