# Aplica las migraciones pendientes del backend BPM a la base indicada por DATABASE_URL.
#
# Uso (PowerShell, desde Software\Backend):
#   .\scripts\aplicar-migraciones.ps1              # usa DATABASE_URL de .env
#   .\scripts\aplicar-migraciones.ps1 -SoloRevisar # solo informa qué falta
#
# Pasos:
#   1. Detecta qué migraciones ya están aplicadas, con el mismo tipo de marca que
#      comprueba el backend al arrancar.
#   2. Hace un backup completo con pg_dump antes de cambiar nada.
#   3. Aplica en orden solo las que faltan. Cada archivo tiene su propio
#      BEGIN/COMMIT: si una falla se revierte entera y el script se detiene.
# Nunca imprime la cadena de conexión.
param(
    [switch]$SoloRevisar,
    [string]$PgBin = "C:\Program Files\PostgreSQL\17\bin"
)
$ErrorActionPreference = "Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)

$url = $env:DATABASE_URL
if (-not $url) {
    $line = Get-Content .env | Where-Object { $_ -match '^DATABASE_URL=' } | Select-Object -First 1
    if (-not $line) { throw "No hay DATABASE_URL en el entorno ni en .env" }
    $url = $line.Substring("DATABASE_URL=".Length).Trim()
}
$psql = Join-Path $PgBin "psql.exe"
$pgDump = Join-Path $PgBin "pg_dump.exe"

function Consultar([string]$sql) {
    $r = & $psql $url -X -A -t -v ON_ERROR_STOP=1 -c $sql 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Falló la consulta de verificación: $r" }
    return ($r | Out-String).Trim()
}

# Marca distintiva que deja cada migración.
$migraciones = [ordered]@{
    "V2__authentication_sessions"     = "SELECT to_regclass('authentication_sessions') IS NOT NULL"
    "V4__checklist_evidence_items"    = "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='checklist_media' AND column_name='checklist_item_id')"
    "V5__daily_operations"            = "SELECT to_regclass('storage_lots_receipt_item_uq') IS NOT NULL"
    "V6__incidents"                   = "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='incident_catalog_code_version_uq')"
    "V7__alerts_notifications"        = "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='alerts_dedup_uq')"
    "V8__training_academy"            = "SELECT to_regclass('training_versions_one_draft_uq') IS NOT NULL"
    "V9__research_cap"                = "SELECT to_regclass('survey_instruments_one_cap_uq') IS NOT NULL"
    "V10__reports_usage"              = "SELECT to_regclass('report_settings') IS NOT NULL"
    "V11__training_assignments_paths" = "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='training_lessons' AND column_name='external_video_id')"
    "V12__accounts_management"        = "SELECT to_regclass('organization_invitations') IS NOT NULL"
    "V13__operations_shifts_stock"    = "SELECT to_regclass('location_shifts') IS NOT NULL"
    "V14__alerts_admin_operates"      = "SELECT position('''CHECKLIST_MISSING'' THEN m.role IN (''ADMIN'',''CHEF'')' in pg_get_functiondef('alert_recipient_authorized(text,uuid,text)'::regprocedure)) > 0"
    "V15__training_lesson_progress"   = "SELECT to_regclass('training_lesson_progress') IS NOT NULL"
    "V16__training_lesson_captions"   = "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='training_lessons' AND column_name='captions_vtt')"
    "V17__normative_assistant"        = "SELECT to_regclass('normative_documents') IS NOT NULL"
    "V18__webhooks"                   = "SELECT to_regclass('webhook_endpoints') IS NOT NULL"
    "V19__training_question_bank"     = "SELECT to_regclass('training_evaluation_draws') IS NOT NULL"
    "V20__training_platform_videos"   = "SELECT to_regclass('platform_training_videos') IS NOT NULL"
}

$pendientes = @()
foreach ($m in $migraciones.Keys) {
    $aplicada = (Consultar $migraciones[$m]) -eq "t"
    Write-Host ("{0,-34} {1}" -f $m, $(if ($aplicada) { "aplicada" } else { "FALTA" }))
    if (-not $aplicada) { $pendientes += $m }
}
if ($pendientes.Count -eq 0) { Write-Host "`nNo falta ninguna migración."; exit 0 }
if ($SoloRevisar) { Write-Host "`nFaltan: $($pendientes -join ', ')"; exit 0 }

$backup = "backup-antes-migraciones-{0:yyyyMMdd-HHmmss}.dump" -f (Get-Date)
Write-Host "`nCreando backup $backup ..."
& $pgDump $url -Fc -f $backup
if ($LASTEXITCODE -ne 0) { throw "Falló el backup; no se aplicó ninguna migración." }
Write-Host "Backup listo ($([math]::Round((Get-Item $backup).Length / 1MB, 1)) MB). Guárdalo fuera del repo."

foreach ($m in $pendientes) {
    Write-Host "Aplicando $m ..."
    & $psql $url -X -q -v ON_ERROR_STOP=1 -f "migraciones\$m.sql"
    if ($LASTEXITCODE -ne 0) {
        throw "Falló $m. Esa migración se revirtió completa; las anteriores quedaron aplicadas. Restaurar con: pg_restore --clean -d <DATABASE_URL> $backup"
    }
}
Write-Host "`nListo: se aplicaron $($pendientes -join ', '). Vuelve a desplegar el backend y el worker en Railway."
