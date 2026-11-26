# Reportes, constancias y registro de uso (RF-09, 16, 17, 27, 28, 29, 30, 39)

Fuente única para backend (Go) y frontend (KMP). Prefijo `/api/v1`. Errores con el formato
uniforme (`errors[]`, `code`, `message`, `fields`, `requestId`; ver [API_ERRORS.md](API_ERRORS.md)).
JSON camelCase; instantes ISO-8601 UTC (`2026-11-10T15:04:05Z`); fechas de calendario
`AAAA-MM-DD`. Requiere la migración `V10__reports_usage.sql`; el arranque de API y worker falla
de forma explícita si falta (`RequireSchema`). No se ejecutan migraciones automáticamente.

## Reglas comunes

- El tenant sale de la membresía autenticada; `{organizationID}` se vuelve a autorizar. Quien no
  pertenece a la empresa recibe `403 ACCESS_DENIED`. Una sede o curso ajeno responde 404.
- Autorización antes que validación de formato.
- **Roles**: `ADMIN` y `SUPER_ADMIN` ven toda la empresa; `CHEF` solo sus sedes asignadas
  (`membership_locations`) y solo en cumplimiento, resumen, KPIs y exportación; `WORKER` no tiene
  acceso a ningún reporte (403).
- **Zona horaria**: el día de una ejecución es la fecha local de su sede (`locations.timezone`);
  si falta o PostgreSQL no la reconoce se usa `America/Lima`. Los períodos son rangos inclusivos de
  fechas locales (`from`, `to`).
- **Cumplimiento**: promedio de `checklist_runs.compliance_percent` (calculado por el servidor al
  enviar) de ejecuciones `SUBMITTED`; cada ejecución pesa igual. Los porcentajes son números con
  2 decimales; `null` si no hay ejecuciones. Solo aparecen días con ejecuciones (lista dispersa).
- Query strings estrictos: parámetro repetido o desconocido da `400 INVALID_REQUEST`.
  `from`/`to` ausentes o inválidos dan `422 VALIDATION_ERROR` con `fields`.
- Límites de período: cumplimiento 92 días; resumen, exportación y constancias 366 días.

## 1. Cumplimiento por día, sede y empresa (RF-16)

`GET /organizations/{organizationID}/reports/compliance?from=2026-11-01&to=2026-11-30[&locationId=uuid]`
Roles: ADMIN, CHEF, SUPER_ADMIN. 200:

```json
{"data":{"from":"2026-11-01","to":"2026-11-30","runs":12,"averageCompliance":86.67,
 "days":[{"date":"2026-11-02","runs":2,"averageCompliance":90.00}],
 "locations":[{"locationId":"uuid","locationName":"Sede Centro","runs":7,"averageCompliance":88.1,
   "days":[{"date":"2026-11-02","runs":1,"averageCompliance":90.0}]}]}}
```

`days` es la serie de la empresa; `locations[].days` la de cada sede. Errores: 403, 404
`REPORT_LOCATION_NOT_FOUND` (sede inexistente, ajena o fuera del alcance del CHEF), 422.

## 2. Exportación CSV y PDF del historial (RF-17)

### 2.1 CSV

`GET /organizations/{organizationID}/reports/compliance/export.csv?from&to[&locationId]`
Roles: ADMIN, CHEF (solo sus sedes), SUPER_ADMIN. Respuesta en streaming
`200 text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="checklists_<from>_<to>.csv"`,
`Cache-Control: no-store`. UTF-8 **con marca BOM** (`EF BB BF`, para que Excel muestre las tildes; los
clientes pueden ignorarla), separador coma, comillas dobles según `encoding/csv`. Primera fila de
encabezado en español, con estas columnas en este orden:

| # | Encabezado | Contenido |
|---:|---|---|
| 1 | `Fecha local` | `dd/MM/aaaa`, día local de la sede |
| 2 | `Enviado el` | `dd/MM/aaaa HH:mm` (24 h) en la hora local de la sede (`locations.timezone`; `America/Lima` si falta) |
| 3 | `Sede` | nombre de la sede |
| 4 | `Control` | nombre de la plantilla |
| 5 | `Área` | área de la plantilla |
| 6 | `Versión de la plantilla` | entero |
| 7 | `Cumplimiento (%)` | número con 2 decimales y punto (`86.50`) |

Cabecera literal: `Fecha local,Enviado el,Sede,Control,Área,Versión de la plantilla,Cumplimiento (%)`.
Sin identificadores internos (UUID de ejecución ni de sede) ni personas. (Primera versión: `runId,localDate,submittedAtUtc,locationId,locationName,templateName,area,templateVersion,compliancePercent`,
con fechas ISO y horas UTC.) Solo ejecuciones `SUBMITTED`, ordenadas por `submittedAt`. **No** incluye personas
(ni nombres ni correos). Los textos que empiezan por `=`, `+`, `-`, `@`, tabulador o retorno de carro se prefijan
con una comilla simple para evitar inyección de fórmulas. Límite: 50 000 filas; si el período las
supera responde `422 EXPORT_TOO_LARGE` (antes de enviar nada) y el cliente debe acortar el período o
filtrar por sede. Los errores previos al primer byte usan el JSON uniforme; si el flujo se corta
después, el archivo queda truncado (el cliente debe validar que la descarga terminó). Cada
exportación se audita (`CHECKLIST_HISTORY_EXPORTED`).

### 2.2 PDF

`GET /organizations/{organizationID}/reports/compliance/export.pdf?from&to[&locationId]`
Mismos roles, parámetros, período (máx. 366 días), alcance de sedes y errores que el CSV (403, 404
`REPORT_LOCATION_NOT_FOUND`, 400/422 de validación, todos con el JSON uniforme). Respuesta
`200 application/pdf`, `Content-Disposition: attachment; filename="checklists_<from>_<to>.pdf"`,
`Content-Length`, `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`. El PDF se arma completo en
memoria antes del primer byte (no es streaming), por eso su límite es menor: **5 000 controles**
(`422 EXPORT_TOO_LARGE` con mensaje propio; para períodos mayores usar el CSV). Documento A4 en español,
generado por el servidor con un escritor PDF mínimo propio (`adapters/pdf`, Helvetica estándar, sin
dependencias): encabezado con empresa, período, sede (o «Todas las sedes») y fecha de generación en hora de
Lima; tres indicadores (controles finalizados, cumplimiento promedio, sedes con controles); tabla
«Cumplimiento por sede» (si hay más de una; hasta 12 filas) y tabla «Detalle de controles» con las columnas
Enviado el, Sede, Control, Área, Versión y Cumplimiento (%); los nombres largos de sede, control y área se parten en varias líneas (hasta 6) para leerse completos; el encabezado se repite en cada página y hay pie
«Página i de n». Las horas son locales de la sede (`dd/MM/aaaa HH:mm`). Un período sin controles produce un PDF
válido con el aviso «No hay controles finalizados en este período.». Sin personas. Se audita como
`CHECKLIST_HISTORY_EXPORTED_PDF`.

## 3. Reporte de cumplimiento por empresa y período (RF-28)

`GET /organizations/{organizationID}/reports/summary?from&to[&locationId]` (ADMIN, CHEF con sus
sedes, SUPER_ADMIN). 200:

```json
{"data":{"from":"2026-11-01","to":"2026-11-30","runs":12,"averageCompliance":86.67,
 "locations":[{"locationId":"uuid","locationName":"Sede Centro","runs":7,"averageCompliance":88.1,
   "incidentsReported":3,"incidentsClosed":2,"incidentsOpenNow":1,
   "temperatureMeasurements":40,"temperatureOutOfRange":2}],
 "areas":[{"area":"COCINA","runs":9,"averageCompliance":85.0}],
 "templates":[{"templateId":"uuid","name":"Higiene","area":"COCINA","runs":9,"averageCompliance":85.0}],
 "incidents":{"reported":3,"closed":2,"openNow":1},
 "temperatures":{"measurements":40,"outOfRange":2}}}
```

`locations` incluye toda sede del alcance (con ceros si no hubo actividad). `incidentsReported`:
creadas en el período; `incidentsClosed`: cerradas (`CLOSED`) en el período; `incidentsOpenNow`:
creadas hasta `to` y que hoy no están `CLOSED`. Temperaturas: mediciones de recepción con criterio
registrado (V5); fuera de rango = menor al mínimo o mayor al máximo guardado en el ítem.

## 4. Panel de KPIs (RF-30)

`GET /organizations/{organizationID}/reports/kpis` (sin query; ADMIN, CHEF con sus sedes, SUPER_ADMIN).

```json
{"data":{"generatedAt":"2026-11-10T15:00:00Z",
 "compliance":{"today":{"averageCompliance":90.0,"runs":2},"last7Days":{"averageCompliance":88.5,"runs":10},"last30Days":{"averageCompliance":null,"runs":0}},
 "pendingChecks":{"assigned":4,"pending":1,"overdue":1},
 "openAlerts":3,"openIncidents":2,
 "training":{"publishedCourses":2,"learners":10,"completedEnrollments":6,"inProgressEnrollments":3,"completionPercent":30.0,"averageBestScore":82.5}}}
```

"Hoy" es la fecha local de cada sede; `last7Days` incluye hoy. `pendingChecks`: asignaciones activas
`DAILY` con plantilla activa y versión publicada en sedes activas; `pending` = sin ejecución
finalizada hoy; `overdue` = pendientes cuya hora límite ya pasó. `openAlerts`: estado
`PENDING|SENT|VIEWED` (CHEF: solo alertas con sede dentro de su alcance). `openIncidents`: estado
distinto de `CLOSED`. `training` es `null` para CHEF; `completionPercent` = completados /
(cursos publicados x personas activas).

## 5. Avance de capacitación (RF-09)

Roles: ADMIN y SUPER_ADMIN (contiene datos de personas); CHEF y WORKER reciben 403.

`GET /organizations/{organizationID}/reports/training/courses` responde 200
`{"data":[{"courseId","code","title","assignable":10,"enrolled":7,"inProgress":3,"completed":4,"completionPercent":40.0,"averageBestScore":82.5}]}`.
Solo cursos `PUBLISHED`. `assignable` = personas con membresía y cuenta activas; `enrolled` = filas de
`training_progress` de esas personas; `averageBestScore` es el promedio del mejor puntaje (`null` si no hay).

`GET /organizations/{organizationID}/reports/training/people?pageSize=25&pageToken=...[&courseId=uuid]`
responde 200 `{"data":[{"userId","displayName","role","publishedCourses":2,"completed":1,"inProgress":1,"averageBestScore":90.0,"lastActivityAt":"..."}],"pagination":{"nextPageToken":""}}`
(`lastActivityAt` puede ser `null`). Ordenado por `userId`; cursor opaco ligado al usuario, empresa y
`courseId`. Con `courseId`, `publishedCourses` es 1 y el avance se limita a ese curso. 404
`TRAINING_COURSE_NOT_FOUND` si el curso no es publicado de la empresa; 400 `INVALID_PAGINATION` para
tamaño/cursor/parámetros inválidos. `pageSize` 1 a 100 (25 por defecto).

## 6. Configuración de reportes

Roles: ADMIN y SUPER_ADMIN. `GET /organizations/{organizationID}/reports/settings` responde
`{"data":{"weeklyEmailEnabled":true,"complianceThreshold":80}}` (valores por defecto si nunca se
configuró). `PUT` a la misma ruta con cuerpo JSON (máx. 1 KB, ambos campos obligatorios, sin campos
extra): `{"weeklyEmailEnabled":false,"complianceThreshold":85.5}` y responde 200 con el mismo formato.
`complianceThreshold`: 1 a 100, hasta 2 decimales; si no, 422 `VALIDATION_ERROR`.

## 7. Constancia interna de cumplimiento (RF-39)

- `POST /organizations/{organizationID}/reports/certificates` (ADMIN y SUPER_ADMIN). Cuerpo
  `{"from":"2026-10-01","to":"2026-10-31"}` (máx. 1 KB, 366 días; `to` debe ser anterior a hoy en Lima).
  El servidor calcula el cumplimiento promedio de **toda** la empresa (todas las sedes) y emite solo si
  `promedio >= umbral` vigente. Responde 201 con la constancia nueva, o **200 con la ya emitida** si
  ese período exacto ya tiene constancia (idempotente; subir el umbral después no la modifica ni la
  reemite).

  ```json
  {"data":{"id":"uuid","periodStart":"2026-10-01","periodEnd":"2026-10-31","averageCompliance":85.0,"threshold":80.0,"runs":120,"verificationCode":"ABCDEFGHIJKLMNOPQRSTUVWX","issuedAt":"2026-11-10T15:00:00Z"}}
  ```

  Errores: 403 (CHEF/WORKER), 422 `VALIDATION_ERROR`, 422 `REPORT_NO_DATA` (sin ejecuciones),
  422 `REPORT_THRESHOLD_NOT_MET` (`fields.averageCompliance` y `fields.threshold` como texto con 2 decimales).
- `GET /organizations/{organizationID}/reports/certificates?pageSize&pageToken` (ADMIN/SUPER_ADMIN):
  `{"data":[constancia...],"pagination":{"nextPageToken":""}}`, más recientes primero.
- **Verificación pública** (sin autenticación): `GET /compliance-certificates/{code}` responde 200
  `{"data":{"valid":true,"organizationName","periodStart","periodEnd","averageCompliance","threshold","issuedAt","verificationCode"}}`.
  El código son 24 caracteres base32 (`A-Z2-7`) aleatorios (120 bits). Código desconocido o mal
  formado: 404 `CERTIFICATE_NOT_FOUND`. No expone identificadores internos. Las constancias son
  inmutables (trigger en PostgreSQL) y se auditan.

## 8. Correo semanal automático (RF-29)

Lo ejecuta el worker (`cmd/worker`) en cada ciclo, solo si el proveedor de correo (Resend) está
configurado. Desde el lunes 08:00 (Lima) programa un correo por ADMIN activo con la semana anterior
(lunes a domingo): controles finalizados, cumplimiento promedio, incidencias reportadas/cerradas/
abiertas, temperaturas fuera de rango y cumplimiento por sede. Sin datos personales de terceros.
Idempotente por (empresa, semana, destinatario) con clave de idempotencia estable hacia el proveedor;
reintentos con espera creciente (6 intentos); el destinatario y la configuración se revalidan antes de
enviar (si dejó de ser ADMIN o se desactivó, la entrega se cancela). Se desactiva por empresa con
`weeklyEmailEnabled=false` (activo por defecto). Tabla `weekly_report_deliveries`. No hay endpoint HTTP.

## 9. Registro de uso para investigación (RF-27)

Roles para registrar: `ADMIN`, `CHEF`, `WORKER` con membresía activa (SUPER_ADMIN sin membresía: 403).
El servidor fija usuario y empresa; el cliente nunca los envía. No se guardan cuerpos, datos
sanitarios ni metadatos (`metadata` siempre `{}`).

`POST /organizations/{organizationID}/usage/events`, cuerpo JSON máx. 32 KB, lote atómico (todo o nada):

```json
{"events":[{"id":"uuid","sessionId":"uuid","type":"MODULE_VISIT","module":"CHECKLISTS","occurredAt":"2026-11-10T15:00:00Z","durationSeconds":42}]}
```

- 1 a 50 eventos; `id` y `sessionId` UUID; `id` único en el lote.
- `type`: `SESSION_START`, `SESSION_END`, `MODULE_VISIT`, `ALERT_VIEWED`, `ALERT_ATTENDED`.
- `module`: `APP|HOME|CHECKLISTS|OPERATIONS|INCIDENTS|ALERTS|TRAINING|REPORTS|SETTINGS`. Obligatorio en
  `MODULE_VISIT`; por defecto `APP` en eventos de sesión y `ALERTS` en eventos de alerta.
- `occurredAt`: dentro de los últimos 7 días y a lo sumo 5 minutos en el futuro.
- `durationSeconds` (0 a 86400) solo en `MODULE_VISIT` y `SESSION_END`.
- La sesión se crea con el primer evento; `SESSION_START` adelanta el inicio y `SESSION_END` fija el fin.
- Idempotencia por `id`: reenviar el mismo evento cuenta como duplicado; el mismo `id` con otro
  contenido o con otro usuario da 409 `IDEMPOTENCY_CONFLICT`; una sesión de otro usuario o empresa da
  409 `USAGE_SESSION_CONFLICT`.
- 200 `{"data":{"accepted":3,"duplicates":0}}`. Errores de campos: 422 con claves `events[i].campo`.

`GET /organizations/{organizationID}/usage/summary?from&to` (ADMIN y SUPER_ADMIN, máx. 92 días, días
en hora de Lima):

```json
{"data":{"from":"2026-11-01","to":"2026-11-30","sessions":40,"endedSessions":35,"activeUsers":8,
 "averageSessionSeconds":540.0,"alertsViewed":12,"alertsAttended":9,
 "modules":[{"module":"CHECKLISTS","visits":120,"uniqueUsers":7,"totalSeconds":5400}],
 "days":[{"date":"2026-11-02","sessions":5}]}}
```

Con `scope=research` el resumen solo incluye a usuarios con participación `ACTIVE` y consentimiento de investigación
vigente (último `consent_records` aceptado sobre el documento `ACTIVE` de `research_consent_documents`, tablas del
módulo research, V9); una revocación o un documento nuevo sin reaceptar excluyen retroactivamente al usuario del
resumen. Sin `scope` (o `scope=operational`) el resumen operativo para ADMIN cubre a todos los usuarios, como antes.
Los eventos se siguen registrando para todos; el filtro se aplica al consultar.

Solo agregados (sin personas). `averageSessionSeconds` usa sesiones con fin, con tope de 24 h; `null` si
no hay.

## Códigos de error propios

| HTTP | code | Cuándo |
|---:|---|---|
| 403 | `ACCESS_DENIED` | Rol sin permiso o usuario ajeno a la empresa |
| 404 | `REPORT_LOCATION_NOT_FOUND` | Sede inexistente, ajena o fuera del alcance |
| 404 | `TRAINING_COURSE_NOT_FOUND` | Curso no publicado de la empresa |
| 404 | `CERTIFICATE_NOT_FOUND` | Código desconocido o mal formado |
| 409 | `IDEMPOTENCY_CONFLICT`, `USAGE_SESSION_CONFLICT` | Uso: UUID reutilizado, sesión ajena |
| 422 | `REPORT_NO_DATA`, `REPORT_THRESHOLD_NOT_MET`, `EXPORT_TOO_LARGE` | Constancia o exportación (CSV > 50 000 filas, PDF > 5 000) |
| 400 | `INVALID_REQUEST`, `INVALID_PAGINATION` | Estructura, parámetros o cursor inválidos |
