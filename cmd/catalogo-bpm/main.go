// Comando catalogo-bpm instala en UNA organización los cinco módulos BPM (ruta de aprendizaje en unidades)
// y el banco oficial de 100 preguntas. Por defecto solo simula: no escribe nada hasta indicar -aplicar.
//
// Uso:
//
//	DATABASE_URL=... go run ./cmd/catalogo-bpm -org <id> [-by <usuario>] [-aplicar]
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

	"github.com/Nidka/BPM-service/internal/features/training"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	org := flag.String("org", "", "id de la organización (obligatorio)")
	by := flag.String("by", "", "usuario que queda en la auditoría (por defecto, un ADMIN activo de la organización)")
	apply := flag.Bool("aplicar", false, "guarda los cambios (sin esta opción solo simula)")
	flag.Parse()
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
