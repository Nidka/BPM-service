# Plan de evidencias privadas de controles

**Objetivo:** permitir al CHEF adjuntar fotos a preguntas de sus revisiones en borrador y permitir a supervisores autorizados consultarlas, sin exponer archivos públicamente.
**Fuentes:** specs/FEATURES.md, CHECKLISTS.md, SECURITY.md, ARCHITECTURE.md; RF-15.
**Arquitectura:** feature media con dominio, aplicación, PostgreSQL/HTTP; R2 en core como capacidad técnica. App compone el panel sin dependencias cruzadas entre media y checklists. Selector nativo por plataforma, UI y validación compartidas.

## Decisiones

- Primera carga: JPEG/PNG, máximo configurado MEDIA_MAX_UPLOAD_BYTES y tres fotos activas por pregunta. No se aceptan SVG, HTML, PDF, vídeo ni tipos no implementados como imagen segura.
- Carga directa mediante PUT firmado con If-None-Match: *, evitando reemplazar una evidencia confirmada. HEAD y lectura acotada comprueban tamaño, MIME real, formato/dimensiones y SHA-256 calculado sobre los bytes; no se confía en x-amz-meta-sha256.
- R2 admite condiciones atómicas de PUT; su API S3 no garantiza SHA-256 FULL_OBJECT. Referencias oficiales: https://developers.cloudflare.com/r2/api/s3/api/ y https://developers.cloudflare.com/r2/api/s3/extensions/ .
- Crear/confirmar/retirar exige CHEF, membresía y sede activa, autor de la revisión, estado DRAFT. ADMIN/SUPER_ADMIN supervisan; CHEF solo consulta sedes asignadas; WORKER no accede a Control.
- UUID y metadatos inmutables permiten renovar la firma/reintentar sin duplicar. Firma nunca persistida ni registrada. Fallos de formato/hash quedan FAILED; la eliminación es lógica/auditada.
- Migración V4 independiente (V3 local de capacitación no se incluye): sustituye vínculo frágil checklist_answer_id por checklist_item_id. Conserva asociación histórica a pregunta; las revisiones finalizadas no cambian. No se modifica V1.
- SaveAnswers usará UPSERT para conservar IDs y actualizará eliminación de respuestas sin romper evidencias. Submit exigirá evidencia AVAILABLE por pregunta cuando corresponda.
- La primera selección/carga de fotos requiere conexión. Un corte permite confirmar/reintentar la misma reserva; tras cerrar la app se puede consultar la reserva y retirar/reseleccionar. No se anuncia que los bytes estén protegidos offline: la cola binaria y la limpieza física programada de huérfanos son otra entrega.

## Contrato API (prefijo /api/v1)

- GET /organizations/{org}/checklist-runs/{run}/items/{item}/evidence: data {files, maxBytes, allowedMimeTypes, maxFiles, uploadAvailable}.
- POST /organizations/{org}/media/uploads: JSON {id,runId,itemId,originalName,mimeType,sizeBytes,sha256}; data {file,upload?}, con upload {url,headers,expiresAt}.
- POST /organizations/{org}/media/{id}/confirm: JSON vacío; data File.
- POST /organizations/{org}/media/{id}/download: JSON vacío; data {url,headers,expiresAt}.
- DELETE /organizations/{org}/media/{id}: eliminación lógica repetible; data File.
- File: {id,runId,itemId,originalName,mimeType,sizeBytes,status,createdAt,uploadedAt?}; no object_key/storage_url.

## Pasos

- [x] Pruebas rojas de permisos, metadatos, reintento y confirmación real; endurecer adaptador R2.
- [x] Migración y feature backend; pruebas PostgreSQL aislado/HTTP y OpenAPI.
- [x] Selector de fotos (tarea paralela independiente, archivos exclusivos); dominio/repositorio/VM y panel compartido.
- [x] Integración en edición/revisión, evidencias obligatorias y proxy cerrado, sin simular confirmación.
- [x] Comprobar suites, web/Android/iOS, UI móvil/escritorio y actualizar documentación/entrega.

## Riesgos que comprobar

Contenido falso con metadata correcta; sobreescritura con firma vigente; respuesta PUT/confirmación perdida; otra cuenta/sede; cambios concurrentes con finalizar; reemplazo de respuestas con evidencia; archivo demasiado grande/imagen descompresiva; firma vencida; R2 no configurado; cancelación/cambio de empresa sin restaurar bytes ni URLs de otro contexto.

## Resultado comprobado (2 de noviembre de 2026)

- Backend: 28 paquetes de prueba aprobados con `go test -race -count=1 ./...`,
  PostgreSQL aislado y `go vet ./...` sin hallazgos; V1 sin modificaciones.
- Frontend: 182 pruebas JVM y 36 Node aprobadas; web, APK Android y framework
  iOS Simulator ARM64 construidos. OpenAPI YAML y referencias válidos.
- Chrome aislado con API/R2 ficticios: selector real de archivo, preview,
  cambios 320/390/768/1440 px, conservación de observaciones y selección pendiente,
  cancelación del cierre y reintento que confirma sin repetir PUT.
- Revisión independiente detectó y corrigió la pérdida de estado al cambiar
  hoja/diálogo. La prueba responsive reprodujo el fallo antes y pasó después.
  Se evitó un segundo diálogo anidado que ocultaba los controles al árbol accesible.
- Integración de código revisada; no equivale a despliegue ni aceptación real en R2.

## Activación pendiente externa

Aplicar V4 una vez en el entorno elegido antes del backend, configurar bucket
privado/CORS y probar con chef y supervisor reales. Railway lo administra el
compañero del usuario; no se modificaron datos ni credenciales de producción.
La cola binaria y la eliminación física programada siguen fuera de este alcance.
