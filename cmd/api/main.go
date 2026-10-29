package main

import (
	"log/slog"
	"os"

	"github.com/eclipse-vlr/linkforge/internal/app"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := app.Run(logger); err != nil {
		logger.Error("application failed", "error", err)
		os.Exit(1)
	}
}
