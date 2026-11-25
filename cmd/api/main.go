package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Nidka/BPM-service/internal/core/app"
)

// workerBinary es el ejecutable del worker dentro de la misma imagen de contenedor.
const workerBinary = "/app/bpm-worker"

func main() {
	// Con BPM_PROCESS=worker la misma imagen arranca el worker; así Railway usa un solo Dockerfile
	// para los servicios API y worker sin configurar un comando de inicio distinto.
	if os.Getenv("BPM_PROCESS") == "worker" {
		if err := syscall.Exec(workerBinary, []string{workerBinary}, os.Environ()); err != nil {
			slog.Error("worker start failed", "error", err)
			os.Exit(1)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}
