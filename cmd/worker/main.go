package main

import (
	"context"
	"github.com/Nidka/BPM-service/internal/core/app"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if e := app.RunWorker(ctx); e != nil {
		slog.Error("worker stopped", "code", "WORKER_START_FAILED")
		os.Exit(1)
	}
}
