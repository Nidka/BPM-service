# Academia BPM (capacitación, video y modo estudio)

Fuente única para backend (Go) y frontend (KMP). Prefijo:
`/api/v1/organizations/{organizationID}/training`. Errores con el formato
uniforme (`errors[]`, `code`, `message`, `fields`, `requestId`; ver
[API_ERRORS.md](API_ERRORS.md)). JSON camelCase, fechas ISO-8601 UTC. El tenant
efectivo sale siempre de la membresía autenticada. El contrato HTTP completo y
sus esquemas están en `internal/core/httpserver/docs/openapi.yaml`.

Requiere la migración `V8__training_academy.sql`; el arranque falla de forma
explícita si falta (`RequireSchema`). No ejecuta migraciones automáticamente.

## Conceptos

- **Curso** (`training_courses`) con versiones (`training_course_versions`).
  Solo la última versión PUBLISHED de un curso PUBLISHED es visible para aprender.
- **Lección**: título, contenido de texto plano con saltos de línea y video
  opcional (`media_file_id`, MIME `video/mp4` o `video/webm`).
- **Pregunta**: 2 a 6 opciones, `correctOption` (índice 0-based), explicación y
  lección opcional.
- **Modo estudio**: repasar lecciones/videos y practicar preguntas con
  retroalimentación inmediata (correcta/incorrecta + explicación). No califica,
  no crea intentos, no otorga XP ni constancia, no cuenta para aprobación. Sí
  actualiza la "última lección vista" para retomar (RF-11).
- **Modo evaluación**: intento calificado por el servidor (DIAGNOSTIC o FINAL).
  Un FINAL aprobado completa el curso, otorga XP y emite constancia.

## Permisos

| Operación | Roles |
|---|---|
| Aprender (endpoints 1–6) | ADMIN, CHEF, WORKER y SUPER_ADMIN |
| Autoría y videos (7–12) | ADMIN y SUPER_ADMIN |

- Curso, lección, pregunta o video de otra organización responde 404 (no revela
  existencia). Quien no pertenece a la organización de la ruta recibe 403.
- El rol sale de `organization_memberships` (o `application_users.platform_role`
  para SUPER_ADMIN). La autorización precede a la validación de formato.

## Endpoints para aprender

1. `GET /training/courses/{courseId}` → 200 con `course`, `version`
   (`id`, `versionNumber`, `passingScore`), `lessons` (con `video` o `null`),
   `questions` (sin `correctOption` ni `explanation`; el DTO ni siquiera tiene esos
   campos), `progress` y `certificate` (o `null`). `video` es `null` si no hay
   video o no está AVAILABLE. 404 `TRAINING_COURSE_NOT_FOUND` si no hay versión
   publicada.
2. `POST /training/courses/{courseId}/lessons/{lessonId}/video` (sin cuerpo) →
   200 `{url, mimeType, expiresAt}`. GET firmado de R2 de 15 minutos,
   `Cache-Control: no-store`. La URL no se registra. 404
   `TRAINING_VIDEO_NOT_FOUND` si la lección no es de la versión publicada o no
   tiene video AVAILABLE.
3. `PUT /training/courses/{courseId}/progress` `{"lastLessonId","completedLessonId"?}` → 200
   `{progress}`. `completedLessonId` (opcional, V15) marca esa lección como completada
   de forma idempotente y también debe ser de la versión publicada. La lección debe ser de la versión publicada
   (`TRAINING_LESSON_NOT_FOUND`). NOT_STARTED→IN_PROGRESS; COMPLETED nunca
   retrocede. Idempotente por naturaleza.
4. `POST /training/courses/{courseId}/study/answers`
   `{"questionId","selectedOption"}` → 200 `{correct, correctOption, explanation}`
   sin persistencia. 422 si `selectedOption` está fuera de rango; 404
   `TRAINING_QUESTION_NOT_FOUND` si la pregunta no es de la versión publicada.
5. `POST /training/courses/{courseId}/attempts`
   (`id`, `clientOperationId`, `versionId`, `attemptType` DIAGNOSTIC|FINAL,
   `startedAt`, `clientCreatedAt`, `answers[]`) → 201 (200 en repetición idéntica)
   con `attempt`, `results[]` (incluye `correctOption` y `explanation`, ya que el
   intento terminó), `progress` y `certificate`.
   - Debe responder todas las preguntas de la versión, cada una una vez (422
     `TRAINING_ANSWERS_INVALID` con detalle en `fields`; no hay calificación parcial).
   - `versionId` distinto de la versión publicada vigente → 409
     `TRAINING_VERSION_OUTDATED`.
   - Puntaje = `round(correctas*100/total)` (.5 hacia arriba, con enteros);
     `passed = score >= passingScore` (el límite exacto aprueba).
   - Mismo `id` + `clientOperationId` + mismo contenido → mismo resultado (200);
     contenido distinto, otra operación con el mismo `id`, o otro usuario → 409
     `IDEMPOTENCY_CONFLICT`. La huella canónica (`request_hash`) incluye usuario,
     organización, curso y respuestas ordenadas.
   - FINAL aprobado: progreso COMPLETED, `bestScore = max`, XP = 10 × correctas,
     emite constancia única por (organización, usuario, versión) con
     `verificationCode` de 24 caracteres base32 generado con `crypto/rand`.
     DIAGNOSTIC solo mueve NOT_STARTED a IN_PROGRESS. El cliente nunca envía
     puntaje, aprobación ni XP; los campos extra se rechazan con 400.
6. Catálogo: se reutiliza `GET /organizations/{id}/workspace` (`courses`). No se
   duplica.

## Endpoints de autoría (ADMIN y SUPER_ADMIN)

7. `GET /training/courses?status=DRAFT|PUBLISHED|ARCHIVED` → `{items:[{id, code,
   title, status, publishedVersionNumber, hasDraft}]}` (máximo 100, por título).
8. `POST /training/courses` `{code,title,description?,estimatedDurationMinutes?}` →
   201 `{course}` en DRAFT. 409 `TRAINING_COURSE_CODE_TAKEN`.
9. `GET /training/courses/{courseId}/draft` → borrador editable (lo crea
   copiando la versión publicada con IDs nuevos, o vacío). Incluye `correctOption`
   y `explanation`.
10. `PUT /training/courses/{courseId}/draft` con `version.rowVersion`
    (409 `TRAINING_DRAFT_CONFLICT`). Reemplaza lecciones y preguntas; ambas listas
    son obligatorias. Límites: título ≤200, contenido ≤20000, ≤50 lecciones, ≤100
    preguntas, 2..6 opciones de ≤300 caracteres sin repetirse. `lessonId` de las
    preguntas debe existir en el mismo cuerpo. `videoMediaId` debe ser un video de
    capacitación AVAILABLE o PENDING_UPLOAD de la organización.
11. `POST /training/courses/{courseId}/publish` → borrador PUBLISHED, versión
    anterior ARCHIVED, curso PUBLISHED. 422 `TRAINING_DRAFT_INCOMPLETE` si falta
    una lección, una pregunta o algún video no está AVAILABLE. Intentos y
    constancias de versiones anteriores no cambian.
12. Videos de capacitación (`location_id` NULL, clave
    `organizations/{org}/training/videos/{uuid}.{mp4|webm}`):
    - `POST /training/videos/uploads` → 201 `{file, upload}`. Repetición con los
      mismos metadatos devuelve la misma reserva con nueva firma; distintos → 409
      `TRAINING_VIDEO_CONFLICT`. El PUT firmado dura 15 minutos y lleva
      `If-None-Match: *` (no sobrescribe).
    - `POST /training/videos/{mediaId}/confirm` → HEAD (tamaño y tipo), cabecera
      para firma de formato (MP4: `ftyp` en offset 4; WebM: `1A 45 DF A3`) y SHA-256
      de todo el objeto en streaming → AVAILABLE; inválido → FAILED y 422
      `TRAINING_VIDEO_INVALID`; objeto aún ausente → 409
      `TRAINING_VIDEO_NOT_READY` (reintentable).
    - Límite `TRAINING_VIDEO_MAX_BYTES` (300 MiB por defecto, máximo 1 GiB).

## Decisiones y desviaciones frente al contrato inicial

- **SUPER_ADMIN y persistencia.** `training_progress`, `training_attempts` y
  `training_certificates` referencian `organization_memberships`; SUPER_ADMIN de
  plataforma no tiene membresía. Puede leer, reproducir videos y estudiar
  (sin persistir), pero `PUT progress` y `POST attempts` responden 403.
- **Forma de la respuesta de `POST /training/courses`.** El contrato dice "201
  curso DRAFT"; se devuelve `{"data":{"course":{id, code, title, description,
  estimatedDurationMinutes, status}}}`, coherente con el resto de respuestas.
- **XP y mejor nota.** `bestScore` es el mejor puntaje de un intento FINAL
  (aprobado o no). El XP solo se otorga con un FINAL aprobado: 10 × correctas y
  nunca disminuye (se conserva el máximo). No hay XP por DIAGNOSTIC.
- **Constancia en la respuesta.** `certificate` es la constancia no revocada más
  reciente del usuario en cualquiera de las versiones del curso; un nuevo FINAL
  aprobado en otra versión emite otra constancia. Por versión es única
  (índice `training_certificates_user_version_uq`).
- **Replay.** Los intentos y las versiones publicadas son inmutables, así que
  `attempt` y `results` se reconstruyen idénticos; `progress` y `certificate`
  reflejan el estado actual. El replay se resuelve antes de comparar la versión:
  un reintento tardío tras publicar una versión nueva devuelve el original, no 409.
  Intentos previos a V8 (sin `request_hash`) responden conflicto al repetirse.
- **Tipos de intento.** La tabla admite `MODULE`; en esta primera entrega la API solo aceptaba DIAGNOSTIC y FINAL (MODULE se habilita en "Asignaciones, rutas, insignias y evaluación por módulo", más abajo).
- **Códigos 404 y errores nuevos.** `TRAINING_COURSE_NOT_FOUND`,
  `TRAINING_LESSON_NOT_FOUND`, `TRAINING_QUESTION_NOT_FOUND`,
  `TRAINING_VIDEO_NOT_FOUND`, `TRAINING_DRAFT_NOT_FOUND` (también para `publish`
  sin borrador). `STORAGE_UNAVAILABLE` (503) si R2 está deshabilitado o falla.
  `IDEMPOTENCY_CONFLICT` es un código nuevo introducido por esta feature.
- **Borrador.** `rowVersion` se serializa como texto (también se acepta número).
  La lista de lecciones y de preguntas es obligatoria (vacía permitida) para que
  una omisión no borre el contenido. `displayOrder` debe ser positivo y único; el
  servidor reasigna 1..n. Las opciones duplicadas en una pregunta se rechazan.
  Reutilizar el UUID de una lección o pregunta existente en otra versión responde
  409 `TRAINING_DRAFT_CONFLICT`.
- **Tamaño del cuerpo.** El `PUT draft` está acotado por
  `HTTP_MAX_JSON_BODY_BYTES` (1 MiB por defecto), inferior al máximo teórico de
  50 lecciones × 20000 caracteres. No se cambió el límite global.
- **Plazo de confirmación de video.** `ConfirmVideo` usa su propio presupuesto de
  2 minutos desligado de `HTTP_REQUEST_TIMEOUT` (20 s) para poder hashear videos
  grandes; el `WriteTimeout` del servidor (30 s) sigue limitando la respuesta, de
  modo que un cliente lento puede reintentar la confirmación (idempotente). Como
  máximo 2 inspecciones simultáneas por proceso.
- **Claves R2.** MEDIA/SECURITY exigen `organizations/...`; la clave de video ya
  cumple esa validación. Además `PresignVideo*`/`InspectVideo` exigen exactamente
  `organizations/{org}/training/videos/{archivo}`. Las firmas de video duran 15
  minutos (máximo de SECURITY.md) independientemente de `R2_SIGNED_URL_TTL`,
  porque una carga grande necesita más tiempo que una foto.
- **Videos fuera de `media`.** Los videos viven en `media_files` pero no tienen
  asociaciones de controles/incidencias, por lo que el servicio `media` no puede
  leerlos ni mutarlos. La identificación es `location_id IS NULL` + prefijo de
  clave de la organización.
- **Constraints.** V8 añade `row_version`, `request_hash`, unicidad de una sola
  versión DRAFT y una sola PUBLISHED por curso, constancia única, índices y
  restricciones (`NOT VALID`) de 2..6 opciones, respuesta en rango y contenido
  ≤20000. Incluye preflight que rechaza datos duplicados sin borrarlos.

## Seguridad

- Tenant desde la membresía; toda consulta filtra `organization_id`.
- La vista para aprender no contiene respuesta ni explicación; `correctOption`
  solo se revela tras responder (estudio o intento terminado) o a ADMIN en autoría.
- Puntaje, aprobación, XP y código de constancia solo los calcula el servidor.
- Las URL firmadas (GET/PUT) no se registran; las respuestas usan `no-store`.
- Auditoría (`audit_events`) con actor, acción y entidad; nunca respuestas ni contenido.

## Web (frontend Node proxy)

`web/training-proxy.mjs` reenvía solo estas rutas bajo
`/backend/api/v1/organizations/{org}/training/...` con validación estricta de
entrada y salida, como `media-proxy.mjs` e `incidents-proxy.mjs`. El video se
reproduce directamente desde la URL firmada de R2 (no pasa por el proxy).

## Pruebas

- Dominio: puntaje y límite, respuestas manipuladas/duplicadas/faltantes,
  transiciones de progreso y XP, validación de borrador, firmas MP4/WebM, código.
- HTTP con repositorio en memoria: permisos por rol, 404 de tenant ajeno,
  ausencia de `correctOption` en la vista de aprendizaje, idempotencia, flujo de
  borrador, publicación y videos, logs sin URL firmadas, errores sanitizados.
- R2: clave de capacitación, PUT condicional de 15 minutos, SHA-256 en streaming.
- PostgreSQL (se omiten sin `TEST_DATABASE_URL` apuntando a `bpm_test`): ciclo de
  vida completo, aislamiento por organización, concurrencia del mismo intento e
  índices de V8.

---

# Asignaciones, rutas, insignias y evaluación por módulo

Amplía la Academia con RF-06 (asignaciones), RF-21 (aviso), RF-10 (insignias y XP),
RF-42 (evaluación por módulo), RF-40 (rutas de aprendizaje), verificación pública de
constancias, catálogo offline y video externo de YouTube. Todo lo anterior de este
documento sigue vigente. Requiere además `V11__training_assignments_paths.sql`
(aplicar después de V8; V7 es necesaria para el aviso). `RequireSchema` falla de
forma explícita si falta V11. Prefijo común de las rutas autenticadas:
`/api/v1/organizations/{organizationID}/training`. Las respuestas siguen el formato
`{"data": ...}`; los listados paginados devuelven
`{"data":{"items":[...]},"pagination":{"nextPageToken":"..."}}` (vacío si no hay más).
Paginación: `pageSize` (1..100, 25 por defecto) y `pageToken` (cursor opaco que solo
vale para el mismo usuario, empresa y filtros). Cualquier parámetro de query no
declarado, repetido o inválido responde 400 `INVALID_REQUEST`. Fechas ISO-8601 UTC.

## Permisos de lo nuevo

| Operación | Roles |
|---|---|
| Crear/listar/cancelar asignaciones, catálogo y edición de insignias, crear/editar/publicar/archivar rutas | ADMIN y SUPER_ADMIN |
| `me/assignments`, `me/achievements`, `me/offline-catalog` | ADMIN, CHEF y WORKER (con membresía; SUPER_ADMIN recibe 403) |
| Listar y ver rutas | ADMIN, CHEF, WORKER y SUPER_ADMIN (quien aprende solo ve rutas PUBLISHED) |
| Intentos MODULE | igual que `attempts`: ADMIN, CHEF, WORKER |
| Verificación pública | sin autenticación |

Un recurso de otra empresa responde 404 (curso, persona, sede, asignación, ruta,
insignia). Quien no pertenece a la empresa de la ruta recibe 403 `ACCESS_DENIED`. La
autorización se evalúa antes que el formato (salvo `PUT achievements` sin `active`,
que responde 422 de inmediato).

## 1. Asignaciones (RF-06)

### POST `/assignments` (ADMIN, SUPER_ADMIN)

Cuerpo (campos extra → 400):

```json
{
  "id": "uuid generado por el cliente",
  "courseId": "uuid",
  "target": { "type": "USER", "userId": "firebase-uid" },
  "dueAt": "2026-12-31T23:59:00Z"
}
```

`target` es uno de: `{"type":"USER","userId":"..."}` (persona activa de la empresa),
`{"type":"ROLE","role":"ADMIN|CHEF|WORKER","locationId":"uuid?"}` (todo el rol; con
`locationId` solo quienes pertenecen a esa sede) o `{"type":"LOCATION","locationId":"uuid"}`
(todas las personas de la sede). No se mezclan campos de otro tipo (422). `dueAt` es
opcional (`null`), debe ser futura y de hasta 5 años.

Respuesta 201 (200 si es la repetición idéntica del mismo `id`):

```json
{ "data": { "assignment": {
  "id": "uuid",
  "course": { "id": "uuid", "code": "M-01", "title": "Manos limpias" },
  "target": { "type": "USER", "userId": "uid", "userName": "Ana Pérez", "role": null, "locationId": null, "locationName": null },
  "dueAt": "2026-12-31T23:59:00Z",
  "status": "ACTIVE",
  "createdAt": "2026-11-10T15:00:00Z",
  "cancelledAt": null
} } }
```

Errores: 403 `ACCESS_DENIED`; 404 `TRAINING_COURSE_NOT_FOUND` (curso ajeno o
inexistente), `TRAINING_ASSIGNEE_NOT_FOUND` (persona que no pertenece a la empresa),
`TRAINING_LOCATION_NOT_FOUND`; 409 `IDEMPOTENCY_CONFLICT` (mismo `id` con otro
contenido) y `TRAINING_ASSIGNMENT_EXISTS` (ya hay una asignación activa igual para el
mismo curso y destinatario); 422 `TRAINING_COURSE_NOT_PUBLISHED` y `VALIDATION_ERROR`
(con `fields`, por ejemplo `target.userId`, `dueAt`).

### GET `/assignments?status=ACTIVE|CANCELLED&courseId=uuid&pageSize&pageToken` (ADMIN, SUPER_ADMIN)

Más recientes primero. Cada elemento tiene la forma de `assignment` anterior.

### POST `/assignments/{assignmentID}/cancel` (ADMIN, SUPER_ADMIN)

Sin cuerpo. 200 con `{"data":{"assignment":{... "status":"CANCELLED","cancelledAt":"..."}}}`.
Idempotente (cancelar una cancelada devuelve su estado). 404 `TRAINING_ASSIGNMENT_NOT_FOUND`.
Tras cancelar puede volver a asignarse el mismo curso.

### GET `/me/assignments?pageSize&pageToken` — "Mis capacitaciones asignadas"

Una fila por curso publicado (si hay varias asignaciones aplicables se usa la de fecha
límite más próxima). Aplican las asignaciones activas dirigidas a la persona, a su rol o
a una sede a la que pertenece.

```json
{ "data": { "items": [ {
  "assignmentId": "uuid",
  "course": { "id": "uuid", "code": "M-01", "title": "Manos limpias", "description": "...", "estimatedDurationMinutes": 15 },
  "dueAt": "2026-12-31T23:59:00Z",
  "assignedAt": "2026-11-10T15:00:00Z",
  "status": "PENDING",
  "progressStatus": "NOT_STARTED",
  "bestScore": null,
  "completedAt": null
} ] }, "pagination": { "nextPageToken": "" } }
```

`status` lo calcula el servidor desde `training_progress`: `COMPLETED` si el curso está
completado (aunque sea después de la fecha límite); si no, `OVERDUE` si `dueAt` ya pasó;
si no, `IN_PROGRESS` (progreso IN_PROGRESS) o `PENDING` (sin iniciar). `progressStatus` es
el estado crudo (`NOT_STARTED|IN_PROGRESS|COMPLETED`).

### Aviso al destinatario (RF-21)

No se crea un mecanismo nuevo: al crear la primera asignación de una empresa se
garantiza (con un bloqueo asesor, una sola vez) una regla de alertas
`TRAINING_ASSIGNED` global, activa, con push habilitado y correo deshabilitado
(`alert_rules`). El evaluador del worker (`features/alerts`) genera la alerta y el
outbox/`notifications` la entrega en el siguiente ciclo (no es instantáneo). Si un
administrador ya configuró o desactivó una regla global de ese tipo, se respeta.
Limitaciones heredadas del evaluador: solo avisa a CHEF y WORKER (no a ADMIN), y solo
a quien cumple el destinatario y pertenece a la sede cuando hay sede. Requiere el
worker en ejecución.

## 2. Evaluación por módulo (RF-42)

`POST /courses/{courseId}/attempts` admite ahora `attemptType: "MODULE"` y el campo
`lessonId` (UUID, **obligatorio y solo** para MODULE; en DIAGNOSTIC/FINAL se rechaza con
422). Las demás reglas son las del intento normal (idempotencia por `id` +
`clientOperationId`, huella canónica, versión vigente, 409 `IDEMPOTENCY_CONFLICT`,
409 `TRAINING_VERSION_OUTDATED`).

- Solo se evalúan las preguntas de esa lección: hay que responder exactamente esas
  (una por pregunta); responder una pregunta de otra lección es 422
  `TRAINING_ANSWERS_INVALID`.
- Lección que no pertenece a la versión publicada del curso (de otro curso, de otra
  empresa o inexistente) → 404 `TRAINING_LESSON_NOT_FOUND`. Lección sin preguntas → 422.
- Puntaje = `round(correctas*100/total de la lección)`; aprueba con `passingScore` de la
  versión. **No completa el curso ni emite constancia**; solo mueve NOT_STARTED a
  IN_PROGRESS.
- XP de módulo: 5 por respuesta correcta si el módulo se aprueba; por lección se
  conserva el mejor resultado (repetir no multiplica el XP).

La respuesta añade `achievements` (ver sección 3) en todos los tipos de intento:

```json
{ "data": { "attempt": {...}, "results": [...], "progress": {...}, "certificate": null,
  "achievements": [ { "id": "uuid", "code": "PERFECT_SCORE", "name": "Puntaje perfecto", "description": "...", "points": 30,
    "criterionType": "PERFECT_SCORE", "criterionThreshold": 1, "active": true, "awardedAt": "2026-11-10T15:00:00Z" } ] } }
```

## 3. Insignias, logros y XP (RF-10)

El catálogo por defecto de la plataforma (`FIRST_COURSE`, `THREE_COURSES`,
`PERFECT_SCORE`, `STREAK_3`) se materializa en `training_achievements` de cada
empresa la primera vez que se necesita (no pisa ediciones). Criterios
(`criterionType` + `criterionThreshold`), evaluados solo por el servidor:
`COURSES_COMPLETED` (cursos completados >= umbral), `PERFECT_SCORE` (intentos FINAL o
MODULE con 100 >= umbral), `STREAK_DAYS` (días UTC consecutivos con algún intento
terminando hoy >= umbral). Se evalúan al registrar cada intento nuevo y se otorgan de
forma idempotente (clave primaria `organization+user+achievement`): reintentos o
repeticiones nunca duplican. `achievements` en la respuesta del intento lista solo las
otorgadas por ese intento (también en su repetición idéntica).

### GET `/me/achievements`

```json
{ "data": {
  "totals": { "experiencePoints": 95, "courseExperiencePoints": 30, "moduleExperiencePoints": 15, "achievementPoints": 50 },
  "achievements": [ { "id": "uuid", "code": "FIRST_COURSE", "name": "...", "description": "...", "points": 20,
    "criterionType": "COURSES_COMPLETED", "criterionThreshold": 1, "active": true, "awardedAt": null } ]
} }
```

`experiencePoints` = curso + módulos + puntos de insignias obtenidas. `achievements` incluye las
activas de la empresa; `awardedAt` es `null` si aún no se logró.
Además trae `streak` (racha de aprendizaje; ver «Racha, XP por intento y siguiente curso (B2)»).

### GET `/achievements` (ADMIN, SUPER_ADMIN)
`{"data":{"items":[<achievement>...]}}` con todas (activas o no).

### PUT `/achievements/{achievementID}` (ADMIN, SUPER_ADMIN)
Cuerpo: `{"name","description","points","criterionThreshold","active"}`; `name` 2..120,
`description` hasta 300, `points` 0..1000, `criterionThreshold` 1..100, `active`
obligatorio. 200 `{"data":{"achievement":{...}}}`; 404 `TRAINING_ACHIEVEMENT_NOT_FOUND`;
422 con `fields`. Una insignia inactiva deja de otorgarse (las ya otorgadas se conservan).

## 4. Rutas de aprendizaje (RF-40)

Una ruta es una secuencia ordenada de cursos de la empresa (hasta 30). Estados
`DRAFT`, `PUBLISHED`, `ARCHIVED`. Quien aprende solo ve `PUBLISHED`; un borrador o una
ruta archivada responde 404 `TRAINING_PATH_NOT_FOUND`. El avance se calcula en el
servidor contando solo cursos PUBLISHED.

Forma de una ruta (`path`):

```json
{ "id": "uuid", "title": "Inducción", "description": "...", "status": "PUBLISHED", "rowVersion": "3", "publishedAt": "2026-11-10T15:00:00Z",
  "courses": [ { "courseId": "uuid", "code": "M-01", "title": "Manos limpias", "position": 1, "courseStatus": "PUBLISHED", "progressStatus": "COMPLETED" } ],
  "progress": { "status": "IN_PROGRESS", "completedCourses": 1, "totalCourses": 2, "percent": 50, "nextCourseId": "uuid" } }
```

`progressStatus` y `progress` son del usuario que consulta (para SUPER_ADMIN, sin
avance). `nextCourseId` es el primer curso no completado (`null` si terminó).

- `POST /paths` (ADMIN, SUPER_ADMIN): `{"id","title","description","courseIds":[...]}`
  (`title` 2..200, `description` hasta 2000, `courseIds` obligatorio, sin repetidos,
  cursos existentes en la empresa; 422 con `fields` como `courseIds[1]`). 201 (200 si es
  repetición idéntica del mismo `id`; otro contenido → 409 `IDEMPOTENCY_CONFLICT`).
  Responde `{"data":{"path":{...}}}` en DRAFT.
- `PUT /paths/{pathID}` (ADMIN, SUPER_ADMIN): `{"title","description","courseIds","rowVersion"}`
  reemplaza la secuencia. `rowVersion` (texto o número) es obligatorio: obsoleto → 409
  `TRAINING_PATH_CONFLICT`; ruta archivada → 409 `TRAINING_PATH_ARCHIVED`. Si la ruta
  está publicada, todos sus cursos deben estar publicados.
- `POST /paths/{pathID}/publish` (sin cuerpo): exige al menos un curso y todos
  PUBLISHED (422 `TRAINING_DRAFT_INCOMPLETE` con `fields.courseIds`); idempotente;
  archivada → 409 `TRAINING_PATH_ARCHIVED`.
- `POST /paths/{pathID}/archive` (sin cuerpo): idempotente.
- `GET /paths/{pathID}`: una ruta (`{"data":{"path":{...}}}`).
- `GET /paths?status=DRAFT|PUBLISHED|ARCHIVED&pageSize&pageToken`: orden por título; quien
  aprende solo puede listar PUBLISHED (otro `status` → 403).

## 5. Verificación pública de constancias

`GET /api/v1/public/training-certificates/{verificationCode}` **sin autenticación**
(ni cabecera `Authorization`, ni query). Limitada por IP: 1 solicitud por segundo con
ráfaga de 10 (429 `RATE_LIMITED` con `Retry-After`), además del límite global. El
código es de 24 caracteres base32 (`^[A-Z2-7]{24}$`, 120 bits), no enumerable.
`Cache-Control: no-store`.

```json
{ "data": { "status": "VALID", "courseTitle": "Manos limpias", "versionNumber": 2,
  "issuedAt": "2026-11-10T15:00:00Z", "organization": "Restaurante Sol", "holder": "Ana P." } }
```

`status` es `VALID` o `REVOKED` (una constancia revocada se informa, no se oculta).
`holder` es el primer nombre y la inicial del primer apellido (`"A."` si solo hay un
nombre). `organization` es el nombre comercial (o la razón social). Nunca se devuelven
identificadores, correo, puntaje ni el código. Código inexistente **o con formato
inválido** → 404 `TRAINING_CERTIFICATE_NOT_FOUND` (indistinguibles).

## 6. Catálogo descargable offline

`GET /me/offline-catalog` (ADMIN, CHEF, WORKER). Un solo paquete con los cursos publicados
asignados al usuario (hasta 50, ordenados por id) para trabajar sin conexión:

```json
{ "data": { "etag": "9f2c...64 hex", "generatedAt": "2026-11-10T15:00:00Z", "courses": [ {
  "course": { "id": "uuid", "code": "M-01", "title": "...", "description": "...", "estimatedDurationMinutes": 15 },
  "version": { "id": "uuid", "versionNumber": 2, "passingScore": 80 },
  "lessons": [ { "id": "uuid", "title": "...", "content": "...", "displayOrder": 1,
    "video": { "mediaId": "uuid", "mimeType": "video/mp4", "sizeBytes": 1234567, "sha256": "64 hex" },
    "externalVideo": null, "offlineAvailable": true } ],
  "questions": [ { "id": "uuid", "lessonId": "uuid", "questionText": "...", "options": ["a","b"], "displayOrder": 1 } ]
} ] } }
```

- Las preguntas **no** traen `correctOption` ni `explanation`; no hay URL alguna.
- `video` (video privado R2 AVAILABLE) lleva `mediaId`, `mimeType`, `sizeBytes` y
  `sha256`: el cliente obtiene la URL con `POST /courses/{courseId}/lessons/{lessonId}/video`,
  descarga, verifica el SHA-256 y guarda el archivo localmente.
- `externalVideo` (YouTube) es solo en línea: `offlineAvailable: false`. Una lección sin
  video o con video privado tiene `offlineAvailable: true`.
- Cabecera `ETag: "<etag>"` y `Cache-Control: private, no-cache`. Enviar
  `If-None-Match: "<etag>"` devuelve 304 sin cuerpo si el contenido no cambió. El `etag`
  es un hash del contenido (no incluye progreso) y cambia si cambia una versión
  publicada, un video o el conjunto de cursos asignados.
- Para calificar un intento offline se usa `POST attempts` (idempotente) al reconectar.

## 7. Video externo de YouTube por lección

Una lección puede tener, además de (en lugar de) el video privado, un video de
YouTube; **como máximo uno de los dos** (`videoMediaId` o `externalVideo`, ambos →
422 `VALIDATION_ERROR` en `lessons[i].externalVideo`).

- Salida (borrador, vista de aprender, catálogo offline): `"externalVideo": {"provider":"YOUTUBE","videoId":"dQw4w9WgXcQ"}`
  o `null`. Nunca se devuelve una URL. El cliente arma
  `https://www.youtube-nocookie.com/embed/{videoId}`. `videoId` cumple `^[A-Za-z0-9_-]{11}$`.
- Entrada en `PUT /courses/{courseId}/draft` (campo de cada lección, opcional/`null`):
  una URL como texto (`"https://youtu.be/dQw4w9WgXcQ"`) o un objeto
  `{"provider":"YOUTUBE","videoId":"..."}` o `{"provider":"YOUTUBE","url":"..."}` (uno
  de los dos, no ambos). El servidor normaliza a `videoId`. URLs admitidas (http/https,
  sin credenciales ni puerto): `youtube.com|www.youtube.com|m.youtube.com/watch?v=ID`,
  `youtu.be/ID`, `youtube.com/shorts/ID` y `youtube-nocookie.com/embed/ID`. Cualquier otro
  dominio o formato inválido → 422.
- Publicar no exige `AVAILABLE` para videos externos (solo para los privados).
- `POST .../lessons/{lessonId}/video` solo firma videos privados; en una lección con video
  externo responde 404 `TRAINING_VIDEO_NOT_FOUND`.

## Códigos de error añadidos

| Estado | Código | Cuándo |
|---|---|---|
| 404 | `TRAINING_ASSIGNEE_NOT_FOUND`, `TRAINING_LOCATION_NOT_FOUND`, `TRAINING_ASSIGNMENT_NOT_FOUND`, `TRAINING_ACHIEVEMENT_NOT_FOUND`, `TRAINING_PATH_NOT_FOUND`, `TRAINING_CERTIFICATE_NOT_FOUND` | Recurso inexistente o de otra empresa |
| 409 | `TRAINING_ASSIGNMENT_EXISTS`, `TRAINING_PATH_CONFLICT`, `TRAINING_PATH_ARCHIVED` | Duplicado activo, `rowVersion` obsoleto, ruta archivada |
| 422 | `TRAINING_COURSE_NOT_PUBLISHED` | Asignar un curso sin versión publicada |
| 429 | `RATE_LIMITED` | Verificación pública o límite global |

## Decisiones y desviaciones (segunda entrega)

- **Esquema.** V11 relaja `training_assignments_target_check` (ahora basta persona, rol
  o sede), añade `cancelled_at/cancelled_by`, un índice único de asignaciones activas
  equivalentes, `training_attempts.lesson_id`, `training_module_results` (mejor
  resultado por módulo), columnas de criterio/vigencia en `training_achievements`,
  `source_attempt_id` en `user_achievements`, `learning_paths` y `learning_path_courses`,
  y `external_video_provider/external_video_id` en `training_lessons` (con CHECK de formato
  y exclusión con `media_file_id`).
- **Catálogo de insignias por defecto.** `training_achievements.organization_id` es
  obligatorio en V1, así que el "catálogo de plataforma" vive en código y se copia por
  empresa de forma perezosa; no hay filas con `organization_id` nulo.
- **Estado de asignación.** `training_assignments.status` solo persiste `ACTIVE` y
  `CANCELLED`; `PENDING/IN_PROGRESS/COMPLETED/OVERDUE` se calculan por usuario. El valor
  legado `COMPLETED` de la tabla no se usa.
- **Una fila por curso en "mis capacitaciones".** Evita duplicados cuando un curso se
  asigna a la persona y a su rol.
- **Repetir una creación de asignación tras vencer su `dueAt`** devuelve 422 (la fecha
  futura se valida antes de buscar la repetición); la creación es una operación en línea
  de administración.
- **XP de módulo** no modifica `training_progress`; vive en `training_module_results`
  y se suma al total en `GET /me/achievements`.
- **Pruebas:** asignación a otra empresa, rol sin permiso, vencimiento, logros una sola
  vez ante reintentos, MODULE con lección ajena, verificación pública inexistente/revocada,
  rutas, catálogo offline y video externo (HTTP con memoria y PostgreSQL real).

## SUPER_ADMIN que también es miembro
Un SUPER_ADMIN con membresía activa usa el rol de su membresía para progreso, intentos (DIAGNOSTIC, MODULE y FINAL), constancias, logros, `me/assignments` y `me/offline-catalog`; por eso estos endpoints ya no devuelven 403 en ese caso (las notas anteriores sobre SUPER_ADMIN aplican solo a quien no tiene membresía). Sin membresía mantiene el comportamiento anterior. Ver `ORGANIZATIONS.md`.

## Progreso por lección y "Lo esencial" (V15)

Requiere `V15__training_lesson_progress.sql`; el arranque falla de forma explícita si falta. Todo es
aditivo: la app anterior sigue funcionando sin enviar ni leer los campos nuevos.

- **Completar una lección.** `PUT /courses/{id}/progress` acepta `completedLessonId` además de
  `lastLessonId` (que sigue siendo obligatorio). Se guarda en `training_lesson_progress` con origen
  `VIEWED`; repetir la marca no cambia nada. Un intento `MODULE` aprobado marca su lección con origen
  `MODULE_PASSED` (un `VIEWED` previo se mejora a `MODULE_PASSED`; un módulo reprobado no marca nada).
  Una lección ajena a la versión publicada responde 404 `TRAINING_LESSON_NOT_FOUND` y un UUID mal
  formado 422 `fields.completedLessonId`.
- **Forma de `progress`** (curso, `PUT progress` y resultado de intentos, incluida la repetición):
  `completedLessonIds` (arreglo, en orden de temario), `completedLessons`, `totalLessons` y `percent`.
  Si `status=COMPLETED`, `percent=100` y todas las lecciones cuentan como completadas, de modo que
  publicar una versión nueva (lecciones con IDs nuevos) no reinicia un curso aprobado; el avance parcial
  de una versión anterior no migra. El catálogo offline nunca incluye progreso (el `etag` no depende de la persona).
- **`GET /me/assignments`** agrega `percent` por curso (mismo cálculo).
- **Lo esencial.** Cada lección admite `keyPoints`: hasta 5 textos de 1 a 200 caracteres en una sola
  línea, sin repetirse (sin distinguir mayúsculas); error 422 en `lessons[i].keyPoints`. En
  `PUT /courses/{id}/draft`, ausente (o `null`) conserva los puntos previos de la lección con el mismo
  `id` y `[]` los borra. La salida (`GET course`, borrador y catálogo offline) siempre trae el arreglo.
  El contenido ya publicado queda con `keyPoints: []` hasta una nueva publicación, y el `etag` offline
  cambia una vez.
- **Pruebas:** `domain` (límites de `keyPoints`, porcentaje y redondeo), HTTP con repositorio en memoria
  (idempotencia, lección ajena/de otra versión, MODULE aprobado/reprobado, COMPLETED → 100, `percent` en
  mis asignaciones, ausente conserva y `[]` borra) y PostgreSQL real (CHECK de 5 puntos, origen
  `MODULE_PASSED`, porcentaje SQL).

## Racha, XP por intento y siguiente curso (B2)

Todo es aditivo y usa V15 (`training_attempts.xp_awarded`, `training_lesson_progress.completed_at`); la
app anterior ignora los campos nuevos.

- **Racha en `GET /me/achievements`.** Agrega `streak {currentDays, longestDays, lastActivityDate,
  activeToday, timezone}`. Un día es un día calendario con algún intento calificado
  (`training_attempts.completed_at`) o lección completada (`training_lesson_progress.completed_at`; la
  fecha no cambia al repetir la marca), contado una sola vez. La zona es `locations.timezone` de la
  primera sede activa asignada a la persona (orden de asignación, nombre, id); sin sede activa, o con
  una zona inválida, `America/Lima`. La racha vigente (`currentDays`) son los días consecutivos que
  terminan hoy o ayer; si la última actividad fue antes de ayer vale 0. `lastActivityDate` (AAAA-MM-DD en
  esa zona, `null` sin actividad) es el último día con actividad aunque la racha esté rota y `activeToday`
  indica que es hoy. Se consultan los últimos 400 días: ni `currentDays` ni `longestDays` pasan de ahí.
  Cálculo puro en `domain.CurrentStreak` (con pruebas de frontera de zona y cambio de hora). **No cambia**
  la evaluación de la insignia `STREAK_3`, que sigue contando días UTC con intentos: la racha visible y la
  insignia pueden diferir por la zona; el otorgamiento de insignias es permanente.
- **XP por intento.** El servidor guarda en `training_attempts.xp_awarded` el XP de curso y módulo que
  sumó ese intento, calculado en `domain.AttemptXP`: un FINAL suma la diferencia con el mayor XP de curso
  que ya tenía, un MODULE aprobado la diferencia con el mayor XP de ese módulo y DIAGNOSTIC o un intento
  reprobado suman 0 (nunca negativo; un FINAL repetido con la misma nota suma 0). `attempt.xpEarned` =
  `xp_awarded` + puntos de las insignias otorgadas por el intento (los puntos vigentes del catálogo);
  la repetición idéntica devuelve el mismo valor. En los intentos anteriores a V15 (`xp_awarded` NULL)
  `xpEarned` se omite. Con esto la suma de `xpEarned` de una persona coincide con su XP total.
- **`outcome.nextCourse`.** La respuesta de un intento trae siempre `nextCourse`: un objeto
  `{pathId, pathTitle, courseId, courseTitle}` tras un FINAL aprobado (también en su repetición, calculado
  con el estado actual) o `null`. Reglas: (1) rutas PUBLISHED que contienen el curso aprobado cuyo
  próximo curso pendiente (`nextCourseId` de la ruta, solo cursos publicados) es otro curso; si varias, la
  publicada más recientemente; las rutas completas o en borrador no cuentan; (2) si ninguna propone, el
  primer curso PENDING u OVERDUE de `me/assignments` (en su orden, el más recientemente asignado primero)
  distinto del aprobado, con `pathId` y `pathTitle` en `null`; (3) si no hay nada, `null`. DIAGNOSTIC,
  MODULE y FINAL reprobado devuelven `null`.
- **Pruebas:** `domain` (racha con frontera de zona y cambio de hora, `AttemptXP`, `SelectNextCourse`),
  HTTP con repositorio en memoria (XP con insignias, repetido en 0, replay, histórico sin `xpEarned`,
  siguiente curso con ninguna/una/varias rutas, ruta completa con fallback a asignaciones y nada, racha
  vigente/rota, unión de lecciones e intentos, zona de la sede) y PostgreSQL real (`xp_awarded`
  persistido, zona de la primera sede activa y unión de ambas tablas, selección de siguiente curso).
