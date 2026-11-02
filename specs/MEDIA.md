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
