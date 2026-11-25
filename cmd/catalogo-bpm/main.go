// Comando catalogo-bpm instala en UNA organización los cinco módulos BPM (ruta de aprendizaje en unidades)
// y el banco oficial de 100 preguntas. Por defecto solo simula: no escribe nada hasta indicar -aplicar.
//
// Uso:
//
//	DATABASE_URL=... go run ./cmd/catalogo-bpm -org <id> [-by <usuario>] [-aplicar]
//
// Con -videos no instala nada: registra los 5 videos de la plataforma (compartidos por todas las organizaciones) y
// comprueba cada objeto en R2 (tamaño, tipo, firma y SHA-256). Solo uno idéntico al aprobado pasa a AVAILABLE y se
// muestra. Requiere las variables R2_* y DATABASE_URL (por ejemplo con `railway run`); no escribe en R2.
//
// Es idempotente: un módulo cuya versión publicada ya usa el banco vigente se omite. Si el módulo ya existe
// publica una versión nueva (la anterior queda ARCHIVED, sin tocar intentos ni constancias) y conserva el video de
// su primera lección. Requiere la migración V19. La cadena de conexión se lee del entorno y nunca se imprime.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/integrations/r2"
	"github.com/Nidka/BPM-service/internal/features/training"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	org := flag.String("org", "", "id de la organización (obligatorio)")
	by := flag.String("by", "", "usuario que queda en la auditoría (por defecto, un ADMIN activo de la organización)")
	apply := flag.Bool("aplicar", false, "guarda los cambios (sin esta opción solo simula)")
	videos := flag.Bool("videos", false, "registra y verifica en R2 los videos de la plataforma (no instala cursos)")
	flag.Parse()
	if *videos {
		verifyVideos()
		return
	}
	if *org == "" {
		fmt.Fprintln(os.Stderr, "falta -org")
		os.Exit(2)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "falta DATABASE_URL en el entorno")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "DATABASE_URL no es válida")
		os.Exit(1)
	}
	defer pool.Close()
	if err = training.RequireSchema(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "esquema incompatible:", err)
		os.Exit(1)
	}
	var name string
	if err = pool.QueryRow(ctx, `SELECT COALESCE(trade_name,legal_name) FROM organizations WHERE id=$1`, *org).Scan(&name); err != nil {
		fmt.Fprintln(os.Stderr, "la organización no existe")
		os.Exit(1)
	}
	if *by == "" {
		err = pool.QueryRow(ctx, `SELECT user_id FROM organization_memberships WHERE organization_id=$1 AND role='ADMIN' AND status='ACTIVE' ORDER BY created_at LIMIT 1`, *org).Scan(by)
		if err != nil {
			fmt.Fprintln(os.Stderr, "la organización no tiene un ADMIN activo; indica -by")
			os.Exit(1)
		}
	}
	report, err := training.InstallCatalog(ctx, pool, *org, *by, !*apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no se pudo instalar:", err)
		os.Exit(1)
	}
	mode := "SIMULACIÓN (nada se guardó)"
	if *apply {
		mode = "APLICADO"
	}
	fmt.Printf("Organización: %s (%s) · %s\n", name, *org, mode)
	for _, r := range report {
		switch {
		case r.Skipped:
			fmt.Printf("  %-8s ya usa el banco vigente: omitido\n", r.CourseCode)
		default:
			fmt.Printf("  %-8s unidades=%d ítems=%d video=%t creado=%t versión=%d\n", r.CourseCode, r.Units, r.Items, r.VideoKept, r.Created, r.VersionNumber)
		}
	}
}

// verifyVideos registra los videos de la plataforma y los comprueba contra R2.
func verifyVideos() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cfg, err := config.Load()
	if err != nil || !cfg.R2.Enabled {
		fmt.Fprintln(os.Stderr, "faltan las variables de R2 (R2_*) y DATABASE_URL en el entorno")
		os.Exit(2)
	}
	storage, err := r2.New(ctx, cfg.R2)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no se pudo preparar R2")
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "DATABASE_URL no es válida")
		os.Exit(1)
	}
	defer pool.Close()
	if err = training.RequireSchema(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "esquema incompatible:", err)
		os.Exit(1)
	}
	results, err := training.VerifyPlatformVideos(ctx, pool, storage)
	for _, r := range results {
		fmt.Printf("  %-12s %-14s %s\n", r.Code, r.Status, r.Detail)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "no se pudo completar la verificación:", err)
		os.Exit(1)
	}
}
