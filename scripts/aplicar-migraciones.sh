#!/usr/bin/env bash
# Aplica las migraciones pendientes del backend BPM (macOS/Linux). Equivale a aplicar-migraciones.ps1.
# Uso (desde la raíz del repositorio):
#   ./scripts/aplicar-migraciones.sh            # revisa, hace backup y aplica lo que falta (pide confirmación)
#   ./scripts/aplicar-migraciones.sh --revisar  # solo muestra qué falta, sin cambiar nada
# Usa DATABASE_URL del entorno o, si no existe, la de .env. Requiere psql y pg_dump de la misma versión
# principal del servidor o superior (por ejemplo: brew install postgresql@16).
set -euo pipefail
cd "$(dirname "$0")/.."

url="${DATABASE_URL:-}"
if [ -z "$url" ] && [ -f .env ]; then url="$(grep -E '^DATABASE_URL=' .env | head -1 | cut -d= -f2-)"; fi
[ -n "$url" ] || { echo "No hay DATABASE_URL en el entorno ni en .env" >&2; exit 1; }

consultar() { psql "$url" -X -Atq -v ON_ERROR_STOP=1 -c "$1"; }

# Comprobación de cada migración (igual que el script de PowerShell).
migraciones=(
  "V15__training_lesson_progress|SELECT to_regclass('training_lesson_progress') IS NOT NULL"
  "V16__training_lesson_captions|SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='training_lessons' AND column_name='captions_vtt')"
  "V17__normative_assistant|SELECT to_regclass('normative_documents') IS NOT NULL"
  "V18__webhooks|SELECT to_regclass('webhook_endpoints') IS NOT NULL"
  "V19__training_question_bank|SELECT to_regclass('training_evaluation_draws') IS NOT NULL"
  "V20__training_platform_videos|SELECT to_regclass('platform_training_videos') IS NOT NULL"
  "V21__alerts_incident_assigned|SELECT to_regprocedure('alert_incident_assigned_supported()') IS NOT NULL"
)

echo "Servidor: $(consultar 'SHOW server_version') · LC_CTYPE=$(consultar "SELECT datctype FROM pg_database WHERE datname=current_database()")"
pendientes=()
for item in "${migraciones[@]}"; do
  nombre="${item%%|*}"; consulta="${item#*|}"
  if [ "$(consultar "$consulta")" = "t" ]; then printf '%-34s aplicada\n' "$nombre"; else printf '%-34s FALTA\n' "$nombre"; pendientes+=("$nombre"); fi
done

if [ "${#pendientes[@]}" -eq 0 ]; then echo "No hay migraciones pendientes."; exit 0; fi
if [ "${1:-}" = "--revisar" ]; then exit 0; fi
if [ "$(consultar "SELECT to_regclass('training_lesson_progress') IS NOT NULL")" != "t" ]; then
  echo "Falta V15 o una anterior: aplícala primero con el procedimiento de specs/MIGRATIONS.md." >&2; exit 1
fi

read -r -p "Se aplicarán: ${pendientes[*]}. ¿Continuar? (escribe SI): " ok
[ "$ok" = "SI" ] || { echo "Cancelado."; exit 1; }

mkdir -p backups
backup="backups/bpm-$(date +%Y%m%d-%H%M%S).dump"
echo "Backup completo en $backup ..."
pg_dump "$url" --format=custom --no-owner --file "$backup"
[ -s "$backup" ] || { echo "El backup quedó vacío; no se aplica nada." >&2; exit 1; }

for nombre in "${pendientes[@]}"; do
  echo "Aplicando $nombre ..."
  psql "$url" -X -q -v ON_ERROR_STOP=1 -f "migraciones/$nombre.sql"
done

echo "Verificación:"
for item in "${migraciones[@]}"; do
  nombre="${item%%|*}"; consulta="${item#*|}"
  [ "$(consultar "$consulta")" = "t" ] || { echo "$nombre no quedó aplicada" >&2; exit 1; }
  printf '%-34s aplicada\n' "$nombre"
done
echo "Listo. Ya se puede desplegar la API y el worker. Restaurar si hiciera falta: pg_restore --clean --no-owner -d \"\$DATABASE_URL\" $backup"
