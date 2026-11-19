# Módulo de investigación CAP (RF-23, RF-24, RF-25, RF-26, RF-50)

Fuente única para backend (Go) y frontend. JSON camelCase, fechas ISO-8601 UTC.
Errores con el formato uniforme (`errors[]`, `code`, `message`, `fields`,
`requestId`; ver [API_ERRORS.md](API_ERRORS.md)). Respuestas correctas:
`{"data": ...}`; listados: `{"data": [...], "pagination": {"nextPageToken": ""}}`
(parámetros `pageSize` 1..100 y `pageToken`; token vacío = última página; cursor
inválido = 400 `INVALID_PAGINATION`). Todas las respuestas llevan
`Cache-Control: no-store`. Los cuerpos JSON rechazan campos desconocidos (400
`INVALID_REQUEST`) y están limitados a 1 MiB.

Requiere `migraciones/V9__research_cap.sql`; el arranque falla de forma explícita
si falta. Autenticación: `Authorization: Bearer <ID token Firebase>`.

## Modelo de acceso (separado de la operación diaria)

| Rol de acceso | Cómo se obtiene | Qué puede |
|---|---|---|
| Participante | Miembro ACTIVE (ADMIN, CHEF o WORKER) de la organización de la ruta | Ver su participación, aceptar/revocar consentimiento, responder |
| Investigador | Concesión de plataforma en `research_access_grants` (la otorga SUPER_ADMIN) | Todo `/api/v1/research/*` salvo las concesiones |
| Gestor de concesiones | `platform_role = SUPER_ADMIN` | `/research/access-grants` |

- Un ADMIN de empresa **no** ve datos de investigación de nadie: ningún rol de
  empresa ni SUPER_ADMIN sin concesión sustituye la concesión. Sin ella: 403
  `RESEARCH_ACCESS_DENIED`.
- Cada acceso de investigador (lecturas, listados, resultados, exportaciones,
  altas) se audita en `audit_events` (`action` = `research.*`, actor y entidad;
  nunca contenido ni respuestas). La exportación registra además número de filas
  y si hubo filtro por empresa.
- Participante: el tenant sale de la membresía; no ser miembro de la organización
  de la ruta es 403 `ACCESS_DENIED`. SUPER_ADMIN sin membresía no es participante.

## Reglas del estudio

- **Participante seudónimo**: se crea al aceptar el consentimiento por primera
  vez, con código `P-XXXXXXXX` generado en servidor con `crypto/rand`; no deriva de
  datos del usuario. Un usuario es un único participante por organización.
- **Consentimiento informado**: documentos versionados (`version`, texto,
  investigador responsable) publicados por investigadores; solo uno vigente.
  Aceptar/revocar agrega un registro inmutable (versión, fecha, investigador
  responsable, aceptación/revocación). El consentimiento es **vigente** si el
  último registro es una aceptación de la versión del documento vigente. Publicar
  un documento nuevo exige volver a aceptar. Revocar marca al participante
  `WITHDRAWN`: no puede responder y sus datos quedan fuera de resultados y
  exportaciones (los registros se conservan).
- **Instrumento CAP**: versionado e inmutable al publicarse (triggers en V9).
  Tipos y dimensión: `MULTIPLE_CHOICE` → `KNOWLEDGE` (2..6 opciones, `correctOption`
  índice 0-based obligatorio al publicar), `LIKERT` → `ATTITUDE` (escala 1..5),
  `FREQUENCY` → `PRACTICE` (escala 1..5). Solo puede existir un instrumento con
  `isCap = true`; al publicarlo se exige **exactamente 8 MULTIPLE_CHOICE, 8 LIKERT y
  10 FREQUENCY**.
- **Entrega**: PRETEST o POSTTEST, una sola por participante, versión y momento.
  Debe responder todas las preguntas, cada una una vez; la versión debe ser la CAP
  publicada vigente. Idempotente por `id` + `clientOperationId` + contenido.
- **Puntajes (calculados por el servidor, 2 decimales, .5 hacia arriba)**:
  `knowledgePercent = correctas*100/total`; `attitudeMean` y `practiceMean` =
  media de los valores 1..5 de la dimensión. El cliente nunca envía puntajes ni
  recibe las respuestas correctas.

## Endpoints del participante

Prefijo `/api/v1/organizations/{organizationID}/research`.

### 1. `GET /participation` (sin query ni cuerpo)
200:
```json
{"data": {
  "participant": {"code": "P-7K2M9QXA", "status": "ACTIVE"},
  "consentDocument": {"version": "v1", "title": "...", "text": "...", "responsibleInvestigator": "...", "publishedAt": "2026-11-10T15:00:00Z"},
  "consent": {"documentVersion": "v1", "accepted": true, "recordedAt": "2026-11-10T15:01:00Z", "responsibleInvestigator": "..."},
  "consentValid": true,
  "survey": {"instrumentCode": "CAP-BPM", "name": "...", "instrumentVersionId": "uuid", "versionNumber": 1,
    "questions": [{"id": "uuid", "order": 1, "dimension": "KNOWLEDGE", "type": "MULTIPLE_CHOICE", "text": "...", "options": ["...", "..."]}]},
  "submissions": [{"moment": "PRETEST", "instrumentVersionId": "uuid", "submittedAt": "..."}]
}}
```
`participant`, `consentDocument`, `consent` y `survey` son `null` cuando no
aplican; `survey` solo viene con `consentValid = true` y hay CAP publicada. Las
preguntas **no** incluyen respuesta correcta. Para Likert/frecuencia, `options`
son las 5 etiquetas (valor = posición + 1).

### 2. `POST /consent`
Cuerpo `{"documentVersion": "v1", "accepted": true}` (ambos obligatorios;
`documentVersion` 1..40 caracteres `[A-Za-z0-9._-]`). 200 con la misma forma que
`GET /participation`. Idempotente (repetir no crea otro registro). Errores: 409
`RESEARCH_CONSENT_DOCUMENT_UNAVAILABLE` (no hay documento publicado), 409
`RESEARCH_CONSENT_VERSION_OUTDATED` (al aceptar, versión distinta de la vigente),
404 `RESEARCH_PARTICIPANT_NOT_FOUND` (revocar sin haberse inscrito), 422
`VALIDATION_ERROR`.

### 3. `POST /submissions`
```json
{"id": "uuid", "clientOperationId": "uuid", "instrumentVersionId": "uuid", "moment": "PRETEST|POSTTEST",
 "clientCreatedAt": "2026-11-10T15:02:00Z",
 "answers": [{"questionId": "uuid", "value": 1}]}
```
`value`: opción múltiple = índice 0-based de la opción; Likert y frecuencia = 1..5.
`clientCreatedAt` no puede estar más de 5 minutos en el futuro. 201 la primera
vez (200 en repetición idéntica):
```json
{"data": {"id": "uuid", "moment": "PRETEST", "instrumentVersionId": "uuid", "submittedAt": "...",
  "scores": {"knowledgeCorrect": 7, "knowledgeTotal": 8, "knowledgePercent": 87.5, "attitudeMean": 4, "practiceMean": 3}}}
```
Errores: 403 `RESEARCH_CONSENT_REQUIRED` (sin participante, consentimiento ausente,
revocado o de otra versión del documento), 409 `RESEARCH_VERSION_OUTDATED`, 409
`RESEARCH_SUBMISSION_EXISTS` (ya existe esa combinación participante/versión/momento),
409 `IDEMPOTENCY_CONFLICT` (mismo `id`/`clientOperationId` con otro contenido), 404
`RESEARCH_SURVEY_UNAVAILABLE`, 422 `RESEARCH_ANSWERS_INVALID` (faltantes, repetidas,
desconocidas o fuera de rango; detalle en `fields`: `answers[i].questionId|value`),
422 `VALIDATION_ERROR` (formato de `id`, `moment`, etc.).

## Endpoints de investigación (prefijo `/api/v1/research`)

Todos exigen concesión de investigador (403 `RESEARCH_ACCESS_DENIED`), salvo
`/access` y las concesiones (solo SUPER_ADMIN).

| # | Método y ruta | Descripción |
|---|---|---|
| 4 | `GET /access` | `{"data":{"canResearch":bool,"canManageGrants":bool}}` (cualquier autenticado) |
| 5 | `GET /access-grants` | SUPER_ADMIN. Items `{userId,email,displayName,grantedAt,revokedAt,active}` |
| 6 | `POST /access-grants` | SUPER_ADMIN. `{"email"}` → 200 la concesión (idempotente, reactiva revocadas). 404 `RESEARCH_USER_NOT_FOUND` |
| 7 | `DELETE /access-grants/{userID}` | SUPER_ADMIN. 204; 404 `RESEARCH_GRANT_NOT_FOUND` |
| 8 | `GET /consent-documents` | Items `{version,title,text,responsibleInvestigator,status(ACTIVE|ARCHIVED),publishedAt}` |
| 9 | `POST /consent-documents` | `{version(≤40), title(≤200), text(≤20000), responsibleInvestigator(≤200)}` → 201. Archiva el vigente. 409 `RESEARCH_CONSENT_VERSION_TAKEN` |
| 10 | `GET /instruments` | Items `{id,code,name,description,isCap,status,createdAt,publishedVersionNumber|null,hasDraft}` por código |
| 11 | `POST /instruments` | `{code,name,description?,isCap?,questions[]}` → 201 borrador (ver más abajo). 409 `RESEARCH_INSTRUMENT_CODE_TAKEN`, 409 `RESEARCH_CAP_EXISTS` |
| 12 | `POST /instruments/import` | Mismo cuerpo que 11 más `source` y `generalData` opcionales (se ignoran). Importa `docs/research/cap-instrument.json` |
| 13 | `GET /instruments/{id}` | `{instrument, versions:[{id,versionNumber,status,rowVersion,publishedAt}]}` |
| 14 | `GET /instruments/{id}/draft` | Borrador con respuestas correctas. 404 `RESEARCH_DRAFT_NOT_FOUND` |
| 15 | `PUT /instruments/{id}/draft` | `{rowVersion, questions[]}` reemplaza todas las preguntas. 409 `RESEARCH_DRAFT_CONFLICT` |
| 16 | `POST /instruments/{id}/versions` | Abre un borrador copiando la última publicada → 201. 409 `RESEARCH_DRAFT_EXISTS` |
| 17 | `POST /instruments/{id}/publish` | → 200 `{"data":{"version":{...}}}`. 422 `RESEARCH_INSTRUMENT_INCOMPLETE` (falta `correctOption`, está vacío o, en CAP, la composición no es 8/8/10; detalle en `fields`) |
| 18 | `GET /organizations` | Items `{id,name}` para filtros |
| 19 | `GET /results?instrumentVersionId=` | Resultados por empresa (RF-26) |
| 20 | `GET /exports/responses.csv?instrumentVersionId=&organizationId=` | CSV anonimizado |
| 21 | `POST /needs` | RF-50: alta |
| 22 | `GET /needs?organizationId=` | RF-50: listado paginado |

Instrumento inexistente: 404 `RESEARCH_INSTRUMENT_NOT_FOUND`; versión: 404
`RESEARCH_VERSION_NOT_FOUND`.

**Pregunta de entrada** (`questions[]` en 11, 12 y 15; sin `id`: el servidor los
asigna y el orden del arreglo define `order`):
```json
{"type": "MULTIPLE_CHOICE|LIKERT|FREQUENCY", "text": "≤500", "options": ["≤300 c/u, 2..6 únicas"], "correctOption": 1}
```
`LIKERT`/`FREQUENCY`: `options` omitido o vacío usa las etiquetas por defecto
(Totalmente en desacuerdo … Totalmente de acuerdo / Nunca … Siempre); si se envía
deben ser exactamente 5 (≤100 c/u) y `correctOption` debe ser `null`. `correctOption`
puede ser `null` en el borrador. Máximo 60 preguntas. `code` 1..50 `[A-Z0-9_-]`
(se normaliza a mayúsculas), `name` ≤200, `description` ≤2000.

**Borrador** (respuesta de 11, 12, 14, 15 y 16):
```json
{"data": {"instrument": {"id","code","name","description","isCap","status","createdAt"},
  "version": {"id","versionNumber","status","rowVersion","publishedAt"},
  "questions": [{"id","order","dimension","type","text","options":[],"correctOption":1}]}}
```
`rowVersion` es entero (se acepta también texto). Cada `PUT` genera `id` nuevos de
pregunta y la versión de fila aumenta; hay que reenviar el `rowVersion` devuelto.

### 19. Resultados consolidados
200 listado paginado por `organizationId`; `instrumentVersionId` opcional (por
defecto la CAP publicada; 404 `RESEARCH_VERSION_NOT_FOUND` / `RESEARCH_SURVEY_UNAVAILABLE`):
```json
{"data": [{"organizationId": "...", "organizationName": "...", "participants": 12,
   "pretest":  {"n": 10, "knowledgeMean": 62.5, "attitudeMean": 4.1, "practiceMean": 3.2},
   "posttest": {"n": 8,  "knowledgeMean": 81.25, "attitudeMean": 4.5, "practiceMean": 4.0},
   "paired":   {"n": 8, "knowledgeDelta": 18.75, "attitudeDelta": 0.4, "practiceDelta": 0.8}}],
 "version": {"id": "uuid", "versionNumber": 1},
 "pagination": {"nextPageToken": ""}}
```
Medias con 2 decimales, `null` si `n = 0`. `participants` cuenta participantes no
retirados; `paired` compara (post − pre) solo a quienes respondieron ambos momentos.
Excluye participantes `WITHDRAWN`. Aparecen las empresas con participantes.

### 20. Exportación CSV anonimizada
`200 text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="cap-v{n}-respuestas.csv"`.
Sin paginación: máximo 20 000 filas (422 `RESEARCH_EXPORT_TOO_LARGE`; filtrar por
`organizationId`, 404 `RESEARCH_ORGANIZATION_NOT_FOUND` si no existe). Columnas:
`participantCode,organizationId,moment,submittedDate,instrumentVersion,k1..k8,a1..a8,p1..p10,knowledgeCorrect,knowledgeTotal,knowledgePercent,attitudeMean,practiceMean`
(`kN` = conocimiento, `aN` = actitud, `pN` = práctica, en orden de pregunta; valores:
índice de opción elegida o 1..5). **Nunca** incluye correo, UID Firebase, nombres,
id de usuario ni id de entrega. La empresa se identifica por su id (UUID opaco) y la
fecha solo al día. Las celdas que empiezan con `= + - @` se neutralizan con `'`.
Se excluyen participantes retirados. Cada exportación se audita.

### 21–22. Necesidades detectadas (RF-50)
`POST /needs`: `{"organizationId","category","description"(≤2000),"detectedAt"?}`;
`category` ∈ `TRAINING, PROCESS, INFRASTRUCTURE, DOCUMENTATION, TECHNOLOGY, SUPPLIERS, OTHER`;
`detectedAt` por defecto ahora, no futura. 201:
```json
{"data": {"id","organizationId","organizationName","category","description","detectedAt","investigatorId","investigatorName","createdAt"}}
```
404 `RESEARCH_ORGANIZATION_NOT_FOUND`, 422 `VALIDATION_ERROR`. `GET /needs`: mismos
items, más recientes primero (`detectedAt`, `id` descendente), filtro opcional
`organizationId`, `pageSize`, `pageToken`.

## Cuestionario CAP oficial

`docs/research/cap-instrument.json` se extrajo literalmente de
`Documentacion/Documentos/1. Encuesta estructurada pre y post uso del aplicativo.docx`:
8 preguntas de opción múltiple (conocimientos), 8 Likert (actitudes) y 10 de
frecuencia (prácticas), con las 5 etiquetas de cada escala. **El documento no
indica la respuesta correcta de cada pregunta**, por lo que `correctOption` quedó
`null`: tras importarlo (`POST /research/instruments/import`) el investigador debe
fijarlas con `PUT /instruments/{id}/draft` antes de publicar. Los "datos generales"
del documento (tipo de establecimiento, cargo, antigüedad, capacitación previa) van
en `generalData` como metadato y no se importan como preguntas puntuadas; el código
del participante lo genera el servidor.

## Decisiones

- Los datos de `survey_*` existentes son globales (instrumentos) o por organización
  (entregas); V9 añade solo columnas, índices, triggers de inmutabilidad y las
  tablas `research_consent_documents`, `research_access_grants` y
  `research_company_needs`. Preflight que rechaza duplicados sin borrar datos.
- Entregas, respuestas y consentimientos son inmutables (trigger); revocar agrega
  un registro y marca `WITHDRAWN`.
- Los accesos denegados no se auditan (solo los exitosos).
- No se aplica un tamaño mínimo de celda a los resultados por empresa; los
  investigadores concedidos pueden ver agregados de empresas con pocos participantes.

## SUPER_ADMIN que también es miembro
La participación en la encuesta CAP exige membresía ACTIVE y no consulta `platform_role`, por lo que un SUPER_ADMIN miembro participa sin cambios. Las concesiones de investigador siguen dependiendo de `platform_role = SUPER_ADMIN`.
