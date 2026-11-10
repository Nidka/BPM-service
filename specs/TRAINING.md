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
3. `PUT /training/courses/{courseId}/progress` `{"lastLessonId"}` → 200
   `{progress}`. La lección debe ser de la versión publicada
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
- **Tipos de intento.** La tabla admite `MODULE`; la API solo acepta DIAGNOSTIC y FINAL.
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
