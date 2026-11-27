# Alertas configurables y bandeja privada

La API requiere V7 después de V1/V2/V4/V5/V6. V3 local no forma parte del despliegue. `alerts.RequireSchema` comprueba columnas, funciones de autorización y restricciones de deduplicación antes de activar rutas. V7 falla ante duplicados históricos de entregas para exigir revisión del operador, sin borrar datos.

Las rutas parten de `/api/v1/organizations/{organizationID}` y requieren correo verificado y sesión vigente. Los administradores de la empresa y SUPER_ADMIN pueden configurar reglas; consultar avisos exige además membresía activa propia. Un administrador nunca puede leer la bandeja de otro miembro. CHEF/WORKER requieren asignación vigente a la sede cuando corresponde. Usuario, organización, rol, sede y fuente se revalidan en SQL para cada consulta y acuse.

| Método y ruta | Resultado |
| --- | --- |
| GET `/alert-rules` | Reglas autorizadas, búsqueda y página |
| POST `/alert-rules` | Regla creada, HTTP 201 |
| PUT `/alert-rules/{ruleID}` | Regla actualizada, HTTP 200 |
| GET `/alerts` | Bandeja personal, HTTP 200 |
| GET `/alerts/unread-count` | `{count: integer}` |
| GET `/alerts/{alertID}` | Detalle propio |
| POST `/alerts/{alertID}/view` | Acuse de lectura con cuerpo `{}` |
| POST `/alerts/{alertID}/attend` | Acuse de atención con cuerpo `{}` |

Listas usan `{data:[],pagination:{nextPageToken}}`; detalle y escrituras `{data: entidad}`. Tamaño predeterminado 25, máximo 100, búsqueda hasta 160 caracteres. Cursores vinculan actor, empresa, recurso, búsqueda, sede y estado. Orden de reglas por nombre/ID; avisos por creación descendente e ID ascendente. `locationId` opcional en reglas filtra exclusivamente esa sede. Estados de bandeja: ALL, UNREAD, VIEWED y ATTENDED.

Las entradas JSON son objetos UTF8 de hasta 512 KiB, rechazan claves repetidas incluso anidadas, campos desconocidos y documentos múltiples. `active`, `emailEnabled` y `pushEnabled` son obligatorios. Crear regla recibe ID UUID estable, sede opcional, nombre de 2–160 caracteres, tipo, configuración y canales. Actualizar recibe los mismos campos excepto ID y exige `rowVersion`; tipo y sede son inmutables. Las escrituras requieren `Idempotency-Key` ASCII de 8–100 caracteres y guardan la respuesta original ligada al actor y cuerpo canónico. Repetir la clave con otro actor/cuerpo produce 409; versión desactualizada produce 409. Nunca se eliminan reglas: se desactivan.

## Tipos y evaluación

- `CHECKLIST_MISSING`: `{afterLocalTime:"HH:mm"}`. Solo asignaciones DAILY activas, plantilla activa con versión PUBLISHED y sede activa. El día usa la zona IANA de la sede y suma un día calendario, incluyendo DST de 23/25 horas. Antes de la hora configurada no genera avisos; una ejecución DRAFT no cumple. Una ejecución SUBMITTED del assignment creada dentro del período impide avisos. Reciben los CHEF y los ADMIN vigentes de esa sede (el ADMIN cubre todas las sedes; sin duplicar avisos). V14 amplía `alert_recipient_authorized` para que el ADMIN pueda ver ese aviso; aplicar V14 antes del backend que lo emite. Asignaciones futuras no generan avisos.
- `TEMPERATURE_DANGER`: `{minimumTemperatureCelsius:number,maximumTemperatureCelsius:number}`, mínimo menor que máximo, hasta dos decimales y magnitud acorde a `NUMERIC(6,2)`. El intervalo es inclusivo. Se evalúan mediciones no nulas de los ítems de la recepción confirmada; reciben ADMIN y CHEF asignados. No existe rango sanitario predeterminado.
- `ORGANIZATION_INACTIVE`: `{inactivityDays:integer1..365}`, sin sede. Última actividad entre creación de empresa, recepción, limpieza, incidencia y envío de control, con fechas del servidor. Login y fechas cliente no cuentan. Se genera un aviso por episodio de N×24 horas a cada ADMIN activo.
- `TRAINING_ASSIGNED`: `{}`. Solo asignaciones ACTIVE de cursos PUBLISHED creadas desde la regla. Usuario y rol, si ambos existen, deben cumplirse simultáneamente. Se dirige a CHEF/WORKER vigentes; una sede exige asignación vigente. No crea cursos ni asignaciones para simular actividad.

- `INCIDENT_ASSIGNED` (V21): no lo produce una regla ni el evaluador. Lo crea `features/incidents` en la misma transacción del comando ASSIGN, para el responsable de la acción correctiva (ADMIN, CHEF o WORKER; no si se asigna a sí mismo). Fuente `INCIDENT` (id de la incidencia) y `actionId` en el snapshot; solo canal push (`pushEnabled`). Es visible para ese responsable y deja de estar vigente cuando la acción sale de PENDING (START), de modo que no se envía un push tardío; sigue en la bandeja como historial. Idempotente por `incident-assigned:<actionId>`. Es de mejor esfuerzo y está protegido por la función marcadora de V21: sin ella (backend desplegado antes de la migración) no se crea el aviso, se registra una advertencia y la asignación se confirma. Un fallo al crearlo se aísla en un savepoint. No emite webhooks.
- `INCIDENT_REPORTED` (V23): lo crea `features/incidents` en la misma transacción del reporte, para cada ADMIN y cada CHEF con la sede (no para quien reportó). Fuente `INCIDENT`. `priority` del reporte: NORMAL (por defecto) solo bandeja; URGENT además push con prioridad alta («Problema urgente: …»). Vigente mientras la incidencia esté OPEN. Idempotente por `incident-reported:<incidentId>:<userId>`. Mejor esfuerzo, protegido por la función marcadora de V23 y aislado en un savepoint. No emite webhooks.

**Texto del aviso en la bandeja.** Al crear un aviso, el evaluador resuelve datos de negocio (sede, producto/categoría, plantilla, curso) y `alerts/domain.BuildText` redacta título y mensaje específicos, sin datos de personas: `TEMPERATURE_DANGER` «Temperatura fuera de rango · {sede}» / «{producto} ({categoría}): {t} °C · {HH:mm local de la sede}» (hora de la recepción); `CHECKLIST_MISSING` «Control pendiente: {plantilla} · {sede}» / «Aún no se registra el control de hoy. Hora configurada: {HH:mm}.»; `TRAINING_ASSIGNED` «Capacitación asignada: {curso}» / «Vence {dd/MM/aaaa}» (o «Complétala en la Academia.» sin vencimiento). Las partes ausentes se omiten, el título se recorta a 160 y el mensaje a 500 runas, y `ORGANIZATION_INACTIVE` conserva su texto genérico. Los avisos ya creados no se reescriben y no hay migración. El push sigue armando su propio texto con `BuildPush`.

**Avisos antiguos.** Al leer la bandeja o un detalle, si un aviso de tipo `TEMPERATURE_DANGER`, `CHECKLIST_MISSING` o `TRAINING_ASSIGNED` conserva exactamente el texto genérico histórico y su fuente se puede resolver (ítem de recepción → producto/medición del snapshot/sede/hora de la recepción, asignación de capacitación → curso/vencimiento, asignación de control → plantilla), se devuelve el texto específico con el mismo `BuildText` (`SpecificLegacyText`). La fila almacenada no se modifica; sin datos suficientes se devuelve el texto guardado. La búsqueda de la bandeja sigue usando el texto almacenado.

`alerts.New(pool,logger)` expone `EvaluateReceipt(ctx,org,receiptID,now)` y `EvaluateScheduled(ctx,now)` para composición por el worker. Las reglas activas se recorren en páginas de 100. Identidad del aviso: regla+versión+fuente+período/episodio+destinatario. Restricción única de PostgreSQL hace segura la reevaluación concurrente. Se conserva snapshot de configuración, canales y medición/período/actividad; editar la regla nunca reescribe avisos anteriores.

## Lectura, atención y entrega

`viewed_at` y `attended_at` son independientes del proveedor. VIEW no revierte ATTEND; ATTEND fija lectura si faltaba. Cada cambio real crea una sola auditoría sin notas, mediciones ni cuerpos. Los acuses son idempotentes por bloqueo de fila; no resuelven el recurso del negocio. SENT histórico sin lectura se presenta como pendiente.

El DTO incluye ID, tipo, título, mensaje, estado, fechas, sede/nombre/zona y `sourceType`, `sourceId`, `sourceParentId`, `sourceAvailable`. `sourceParentId` de RECEIPT_ITEM apunta a su recepción y el de TRAINING_ASSIGNMENT al curso (`training_assignments.course_id`, UUID); en los demás tipos es `null`. Una fuente completada conserva historia propia autorizada y devuelve `sourceAvailable:false`; revocar membresía, rol o sede retira acceso. Desactivar una regla no oculta historia.

V7 publica funciones SQL `alert_recipient_authorized(text,uuid,text)` para alcance histórico privado y `alert_source_pending(text,uuid)` para vigencia de la fuente. Notifications usa ambas antes de I/O externo sin importar este módulo. La creación del aviso y el evento `notifications.alert.ready.v1` es atómica mediante `core/outbox.Enqueue`; payload `{}` y clave `alert:<ID>` no contienen datos personales ni tokens. El dispatcher crea los destinos por canal usando el snapshot y nunca modifica lectura/atención. La ausencia de proveedor no equivale a envío. Dispositivos, proveedores, leases y reintentos pertenecen a notifications/core/outbox.

## Verificación

Pruebas PostgreSQL crean un esquema propio en `bpm_test`, aplican V1/V2/V4/V5/V6/V7 y lo eliminan al terminar. Cubren aislamiento de destinatario, revocación, cursores, acuses concurrentes, idempotencia/versiones, deduplicación concurrente, snapshot, límites térmicos, mediciones nulas, DST, DRAFT/SUBMITTED, conjunción de destinatarios y episodios de actividad servidor. No se envía correo ni push real.
