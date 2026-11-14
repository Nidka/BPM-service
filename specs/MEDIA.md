# Evidencias privadas de controles

## Alcance y permisos

La feature `media` permite adjuntar fotos JPEG/PNG a una pregunta de un control.
La composición está en `internal/core/app/container.go`; el dominio y los casos
de uso no conocen el SDK S3 ni HTTP. R2 implementa el puerto técnico compartido.

- CHEF: consulta revisiones de sus sedes activas; solo carga, confirma o retira
  fotos de una revisión propia en DRAFT.
- ADMIN y SUPER_ADMIN: consulta y descarga para supervisión, sin responder ni
  modificar controles ajenos.
- WORKER: no accede a controles ni a sus evidencias.

Cada operación vuelve a comprobar usuario, empresa, membresía, sede, revisión y
pregunta de esa versión publicada. No se confía en rol, ruta ni datos locales.
La autorización se repite después de leer R2, antes de confirmar el registro.

## Ciclo de carga

1. El cliente selecciona una foto, calcula SHA-256 y reserva con un UUID estable.
2. El API guarda metadatos inmutables y devuelve un PUT firmado de corta duración.
3. El cliente envía directamente a R2, sin token Firebase ni cookies.
4. `confirm` realiza HEAD y GET acotado, verifica tamaño, MIME detectado, imagen
   decodificable, dimensiones y SHA-256 de los bytes. La metadata SHA del cliente
   no prueba integridad. Solo entonces el estado pasa a AVAILABLE.
5. La descarga requiere nueva autorización y un GET firmado temporal. El cliente
   lee la imagen bajo demanda, sin persistir la firma.

El PUT firmado incluye `If-None-Match: *`: no puede reemplazar un objeto que ya
existe. Un 412 o una respuesta perdida exige confirmar el mismo UUID; no se crea
otra reserva a ciegas. La repetición con metadatos distintos devuelve conflicto.
La confirmación de contenido inválido marca FAILED. Retirar una foto es una
transición lógica DELETED, repetible y auditada; tras finalizar el control no se
permite retirarla.

## Límites y finalización

Máximo tres fotos activas por pregunta, contando pendientes y fallidas hasta
retirarlas. `MEDIA_MAX_UPLOAD_BYTES` limita cada archivo: 10 MiB por defecto,
hasta 25 MiB configurables. La política pública refleja la configuración real,
intersectando los MIME habilitados con JPEG/PNG. No se aceptan PDF, SVG, HTML,
vídeo ni WebP en esta entrega.

La inspección limita cada dimensión a 8000 píxeles y el total a 16 megapíxeles,
con dos decodificaciones simultáneas por proceso. Estos son límites técnicos,
no criterios sanitarios. Los límites de temperatura y otras mediciones proceden
de la plantilla publicada, nunca se inventan en la interfaz.

Una plantilla puede exigir foto ante No cumple o una medición fuera de sus
límites. Finalizar bloquea y consulta la revisión y cuenta únicamente AVAILABLE
para la misma pregunta y sede. Cargar/retirar utiliza el mismo bloqueo de
revisión, por lo que no puede competir con la finalización. `evidenceCounts` es
una proyección de lectura; el cliente no la puede enviar como autoridad.

## Persistencia y migración

Aplicar `V4__checklist_evidence_items.sql` después de V2 y antes de arrancar este
backend. V3 de capacitación sigue siendo un borrador local ajeno a esta entrega;
no se publica ni se aplica. Las versiones omitidas no se reutilizan.

V4 copia las asociaciones existentes desde la respuesta hacia la pregunta y
sustituye `checklist_answer_id` por `checklist_item_id`. Las asociaciones antiguas
sin respuesta mantienen el vínculo a revisión y un item nulo; no se atribuyen
arbitrariamente a una pregunta. SaveAnswers conserva IDs mediante UPSERT y
elimina solo respuestas omitidas. V1 permanece inmutable.

El arranque comprueba V4 y falla de forma explícita si falta. No ejecuta
migraciones automáticamente. El campo heredado `storage_url`, obligatorio en V1,
contiene una referencia lógica `r2://...`, nunca una URL pública o firmada.
Metadatos, relaciones y auditoría se guardan en una misma transacción.

## Configuración y despliegue

El bucket debe ser privado, sin dominio público. Usar las variables R2 ya
definidas en `.env.example`; no publicar credenciales en archivos de prueba.
En CORS del bucket permitir solo los orígenes web propios y métodos GET, PUT,
HEAD. Permitir Content-Type, If-None-Match, x-amz-meta-sha256, Cache-Control y
Content-Disposition. No usar cookies ni autorización de la app para R2.

Orden de entrega: respaldo y aplicación única de V4 con ON_ERROR_STOP,
backend compatible, frontend. No mezclar binarios antiguos que escriban el
vínculo retirado. La API no registra archivos, tokens ni firmas; no copiar URLs
firmadas a logs de diagnóstico. Comprobar primero en un entorno de prueba.

## Verificación y límites conocidos

Las pruebas cubren roles y sedes, revocación durante inspección, metadatos
inmutables, reintentos, límite concurrente de tres archivos, contenido falso o
truncado, SHA real, respuesta ausente, retiro y bloqueo de finalización sin foto.
PostgreSQL de prueba usa schemas aislados; los dobles S3 no sustituyen la prueba
de aceptación con el bucket R2 y el dispositivo reales.

Esta entrega carga fotos con conexión. Las respuestas mantienen su cola local,
pero los bytes de las fotos no están protegidos sin conexión y se descartan al
cerrar el panel. La limpieza física programada de archivos fallidos, retirados u
huérfanos y una cola binaria offline quedan para otra entrega; no se simulan.

Referencias técnicas: [R2 S3](https://developers.cloudflare.com/r2/api/s3/api/),
[PUT condicional](https://developers.cloudflare.com/r2/api/s3/extensions/),
[firmas](https://developers.cloudflare.com/r2/api/s3/presigned-urls/) y
[CORS](https://developers.cloudflare.com/r2/buckets/cors/).

## Evidencia de incidencias

La carga admite un único destino explícito: revisión/pregunta o incidencia con
etapa BEFORE/AFTER. AFTER exige la acción de esa incidencia. Se reutilizan el
mismo servicio, adaptador R2, confirmación de bytes, descarga privada y límites
de nuevas reservas; el detalle del recorrido está en INCIDENTS.md.

Los archivos deben pertenecer a la misma sede física que el agregado autorizado
y tener exactamente una asociación entre controles, incidencias y limpieza. Un
archivo heredado con varios destinos queda bloqueado para listar, descargar o
mutar, y tampoco habilita un cierre. No se elimina ni reasigna automáticamente.
BEFORE lo agrega el reportero o supervisor antes de resolver. AFTER lo agrega
solo el responsable CHEF/WORKER durante la atención. Resolver/cerrar impide
retirar o modificar esa evidencia. Se toma el mismo bloqueo de incidencia que
la validación de cierre.

Las nuevas reservas siguen limitadas a tres fotos por etapa y acción. La lectura
de incidencias conserva galerías históricas con más fotos; el límite de altas
no se usa para ocultar información anterior. Las acciones canceladas conservan
lectura histórica para sus responsables que aún tienen acceso a la sede.

## Videos de capacitación

La Academia BPM reserva videos MP4/WebM sin sede en `media_files` con claves bajo
`organizations/{org}/training/videos/`. Este servicio de evidencias no los lee ni
los muta (no tienen asociación de control, incidencia o limpieza); los gestiona la
feature `training` con el puerto `contracts.VideoStorage`. Ver
[TRAINING.md](TRAINING.md).

## Fotos de limpieza

Las ejecuciones de limpieza (`cleaning_media`, V1; sin migración nueva) admiten fotos JPEG/PNG con el mismo flujo privado:
reserva, PUT firmado a R2, `confirm` (HEAD y GET acotado, SHA-256 y decodificación reales) y descarga con GET firmado.
Mismos límites: `MEDIA_MAX_UPLOAD_BYTES`, 8000 px por lado, 16 megapíxeles, máximo **3 fotos activas por ejecución**
(contando pendientes y fallidas hasta retirarlas). No se aceptan PDF, SVG, WebP ni vídeo.

| Método y ruta | Notas |
|---|---|
| `GET /cleaning-executions/{executionID}/evidence` | `{data:{files, maxBytes, allowedMimeTypes, maxFiles, uploadAvailable}}`, igual que la lista de controles; cada archivo trae `cleaningExecutionId` y `runId`/`itemId` vacíos |
| `POST /media/uploads` | cuerpo de siempre con `cleaningExecutionId` en lugar de `runId`+`itemId` o `incidentId`; mezclar destinos = 422 `fields.cleaningExecutionId` |
| `POST /media/{mediaID}/confirm` | sin cambios |
| `POST /media/{mediaID}/download` | sin cambios (ticket temporal; no persistir la URL) |
| `DELETE /media/{mediaID}` | retiro lógico `DELETED`, repetible y auditado |

Cuerpo de reserva para limpieza: `{id, cleaningExecutionId, originalName, mimeType, sizeBytes, sha256}` (sin `runId`, `itemId`,
`incidentId`, `actionId`, `stage`). Respuesta `{data:{file, upload:{url, headers, expiresAt}}}`; `upload` es nulo si ya estaba
`AVAILABLE`.

Permisos (se reevalúan en cada llamada, también tras leer R2 antes de confirmar):

- Leer y descargar: ADMIN/SUPER_ADMIN (supervisión), CHEF de una sede asignada, y WORKER solo si es el responsable de esa ejecución.
- Reservar, confirmar y retirar: únicamente el responsable de la ejecución (CHEF o WORKER) y solo durante las **24 horas siguientes al registro**
  (`createdAt` de la ejecución); después la evidencia queda de solo lectura (403 `ACCESS_DENIED`). ADMIN supervisa pero no adjunta.
- Ejecución o sede de otra empresa: 403/404 como en controles; la sede debe estar activa.

Un archivo con más de un destino (control, incidencia o limpieza) sigue bloqueado para listar, descargar y mutar.
Las pruebas cubren responsable, otro worker, CHEF de otra sede, ADMIN, tenant ajeno, ventana de 24 horas, límite de tres, reintento
idéntico y con metadatos distintos, PDF rechazado, confirmación por otro usuario y retiro.
