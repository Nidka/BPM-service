# Hechos del backend para cerrar los pendientes del rol WORKER (A-F)

Relevado el 2026-11-20 sobre `main` @ `16e5b75` (solo lectura; no se modificó código).
Rutas relativas a `Software/Backend`. `T/` = `internal/features/training`, `O/` = `internal/features/operations`.

## 0. Estado de git

- `git status -sb` -> `## main...origin/main` (sin `ahead`/`behind`, árbol limpio dentro de Backend).
- `git fetch && git log --oneline -3 origin/main` -> `16e5b75 style: ordenar imports en alertas (gofmt)`, `1227fc9 Merge fix/aceptar-cuerpo-vacio-json`, `756344f fix: aceptar {} como cuerpo vacío...`.
- HEAD local = `16e5b75` = origin/main: **local NO está atrasado** (no se hizo pull).

## 1. Migraciones, despliegue y pruebas

**Herramienta y nombres.** No hay runner (`ls cmd` = `api`, `worker`; no existe `cmd/migrate` ni tabla `schema_migrations` en el código). Archivos `migraciones/V<n>__<descripcion>.sql`, solo hacia adelante, cada uno con su `BEGIN/COMMIT` (`specs/MIGRATIONS.md:7-27`).
Existentes: V1 (baseline), V2, V4..V14. **V3 está reservada y no se usa** (`MIGRATIONS.md`). **Última = `V14__alerts_admin_operates.sql`; la siguiente es `V15`.**

**Cómo se aplican: manual.** Citas:
- `specs/MIGRATIONS.md:36-38`: "Hasta implementar el runner, la ejecución es manual y debe usar `ON_ERROR_STOP=1`; Railway no debe ejecutar automáticamente el baseline sobre la base ya creada."
- `RAILWAY.md` (sección 5): "No añadir todavía un **Pre-deploy Command**: el runner versionado de migraciones aún no está implementado... Cuando exista `cmd/migrate`, se añadirá como paso previo".
- `RAILWAY.md` (flujo automático): push/merge a `main` -> GitHub Actions (`.github/workflows/ci.yml`) -> Railway (Wait for CI) construye el `Dockerfile` y despliega -> healthcheck `/ready`. **El deploy NO corre migraciones.**
- Script de ayuda (PowerShell): `scripts/aplicar-migraciones.ps1` (detecta pendientes con una "marca" SQL por migración, hace `pg_dump` y aplica en orden; `-SoloRevisar`). **Hay que añadir una entrada para V15 en su tabla `$migraciones`** (líneas ~38-52 del script).
- Orden obligatorio: **migrar primero, desplegar después.** `internal/core/app/container.go:74` llama `training.RequireSchema` (`T/adapters/postgres/repository.go:30-71`): si falta una columna/índice que ese método consulta, **el arranque falla y Railway no promueve la versión** (queda la anterior). Si el código nuevo usa columnas nuevas pero `RequireSchema` no las exige, arrancará y fallará en runtime (500) -> conviene añadir la nueva columna/tabla a `RequireSchema` **y** aplicar V15 en Railway antes de mergear a `main`.
- `AGENTS.md`: "`V1` es la línea base ya desplegada. Los cambios posteriores deben ser archivos nuevos e incrementales"; no se sabe desde el repo qué migraciones están aplicadas hoy en producción (RAILWAY.md solo certifica V2 el 18/10/2026; ver `-SoloRevisar`).

**Pruebas.**
- `make test` = `go test ./...`; `make test-race` = `go test -race ./...`; `make check` = fmt+vet+test (`Makefile`). Antes de entregar (`AGENTS.md`): `gofmt`, `go vet ./...`, `go test ./...`, `go test -race ./...`, `git diff --check`. Go 1.27.2 local (verificado).
- **No necesitan Postgres por defecto.** Con `TEST_DATABASE_URL` sin definir, los tests PG hacen `t.Skip("TEST_DATABASE_URL required")`. Ejecuté `go test ./internal/features/training/... ./internal/features/operations/...` sin DB: todo `ok` (los PG se omiten).
- Con DB: URL a una base llamada exactamente **`bpm_test`** (si no, `t.Fatal`), crean un **schema aislado por test** (`CREATE SCHEMA training_<nanos>`) y aplican migraciones desde `migraciones/`: training aplica `V1, V7, V8, V11` (lista en `T/adapters/postgres/repository_test.go:56`; **hay que añadir `V15` ahí**). CI (`.github/workflows/ci.yml`) levanta `postgres:18` con `bpm_test` y exporta `TEST_DATABASE_URL`; su paso "Initialize isolated CI database" aplica solo V1,V2,V4..V7 (los tests de training/operations crean su propio schema). Local: `make db-up` (compose.yaml, puerto 127.0.0.1:5433, DB `bpm`; habría que crear `bpm_test`).
- No hay sqlmock/pgxmock/testcontainers en `go.mod`.

## 2. Módulo Training (Academia)

Prefijo `/api/v1/organizations/{organizationID}/training`; envoltorio `{"data":...}`; listas `{"data":{"items":[]},"pagination":{"nextPageToken":""}}`. JSON camelCase, fechas ISO-8601 UTC. Contrato OpenAPI: `internal/core/httpserver/docs/openapi.yaml` (schemas `TrainingProgress` ~3395, `TrainingCourseView` ~3440).

### 2.1 Tablas (V1 + V8 + V11)

- `training_courses(id, organization_id, code, title, description, estimated_duration_minutes, status DRAFT|PUBLISHED|ARCHIVED, audit cols)` UNIQUE(org,code) — `V1:757`.
- `training_course_versions(id, organization_id, course_id, version_number, passing_score 0..100 def 80, status DRAFT|PUBLISHED|ARCHIVED, published_at, row_version[V8], audit)` — `V1:777`; índices únicos parciales: 1 DRAFT y 1 PUBLISHED por curso (`V8:30-33`).
- `training_lessons(id, organization_id, course_version_id, title, content TEXT ≤20000[V8 NOT VALID], display_order, media_file_id, external_video_provider, external_video_id [V11], audit)` UNIQUE(course_version_id, display_order); CHECK un solo video (`V1:799`, `V11:7-12`). **No hay columna de resumen/puntos clave.**
- `training_questions(id, organization_id, course_version_id, lesson_id NULL, question_text, options JSONB 2..6, correct_option, explanation, display_order, audit)` — `V1:821`.
- `training_assignments(id, org, location_id, course_id, target_user_id, target_role, due_at, status ACTIVE|CANCELLED, cancelled_at...)` — `V1:846`, `V11`.
- `training_attempts(id, org, course_version_id, user_id, attempt_type DIAGNOSTIC|MODULE|FINAL, score, passed, started_at, completed_at, client_operation_id, client_created_at, request_hash[V8], lesson_id[V11], audit)` — `V1:874`. **No tiene columna de XP.** Índice `training_attempts_user_day_idx (org,user_id,completed_at)` (`V11:36`).
- `training_attempt_answers(attempt_id, question_id, selected_option, is_correct)` — `V1:901`.
- `training_progress(PK org,user,course; last_lesson_id, status NOT_STARTED|IN_PROGRESS|COMPLETED, best_score, experience_points, completed_at)` — `V1:921`. **Una sola fila por usuario+curso; no hay progreso por lección** (solo `last_lesson_id`).
- `training_module_results(PK org,user,lesson_id; course_id, best_score, passed, experience_points)` — `V11:39`. Único dato por lección hoy (solo si el usuario rindió intento MODULE).
- `training_certificates(...verification_code, issued_at, revoked_at)` UNIQUE(org,user,version) — `V1:948`, `V8:36`.
- `training_achievements(id, org, code, name, description, points, criterion_type COURSES_COMPLETED|PERFECT_SCORE|STREAK_DAYS, criterion_threshold 1..100, active)` y `user_achievements(PK org,user,achievement; awarded_at, source_attempt_id)` — `V1:972-989`, `V11:62-72`.
- `learning_paths(id, org, title, description, status, row_version, published_at)` y `learning_path_courses(PK path_id,course_id; position)` — `V11:75-100`.
- Auditoría: `audit_events(org, actor_user_id, action, entity_type, entity_id, metadata '{}')` — `V1:159`.

### 2.2 Rutas (`T/adapters/http/handler.go:38-55`, `plus.go:21-35`) y roles

Aprender (ADMIN/CHEF/WORKER/SUPER_ADMIN; persistir exige membresía real: `Actor.CanPersist`, `T/domain/model.go:73`):
| Método y ruta | Handler (file:line) | Caso de uso |
|---|---|---|
| `GET /courses/{id}` | `adapters/http/learn.go:12` | `application/learn.go:13` -> `postgres/learn.go:24 CourseView` |
| `PUT /courses/{id}/progress` | `learn.go:41` | `learn.go:75 SaveProgress` |
| `POST /courses/{id}/study/answers` | `learn.go:59` | `learn.go:108` (sin persistir) |
| `POST /courses/{id}/attempts` | `learn.go:81` | `application/attempts.go:46 SubmitAttempt` |
| `GET /me/assignments` | `plus.go:162` | `application/assignments.go:129` |
| `GET /me/achievements` | `plus.go:185` | `application/achievements.go:49` |
| `GET /paths`, `GET /paths/{id}` | `plus.go:350`, `:336` | `application/paths.go:227/257` |
| `GET /me/offline-catalog` | `plus.go:199` | `application/offline.go:25` |

### 2.3 Formas JSON (DTO con tags)

- **Course detail** `courseViewDTO` (`adapters/http/dto.go:203`): `{course{id,code,title,description,estimatedDurationMinutes,[status]}, version{id,versionNumber,passingScore}, lessons[learnLessonDTO], questions[learnQuestionDTO], progress progressDTO, certificate|null}`.
  - `learnLessonDTO` (`dto.go:183`): `{id,title,content,displayOrder,video{mediaId,mimeType,sizeBytes}|null,externalVideo{provider,videoId}|null}`.
  - `learnQuestionDTO` (`dto.go:195`): `{id,lessonId|null,questionText,options[],displayOrder}` (sin respuesta ni explicación; por diseño).
  - `progressDTO` (`dto.go:142`): `{status,lastLessonId|null,bestScore|null,experiencePoints,completedAt|null}`.
- **Guardar progreso** `progressBody` (`dto.go:26`): `{"lastLessonId":"uuid"}` (JSON estricto: campo desconocido -> 400). Respuesta `{"data":{"progress":progressDTO}}`. Lección debe ser de la versión publicada (404 `TRAINING_LESSON_NOT_FOUND`).
- **Intento** `attemptBody` (`dto.go:40`): `{id,clientOperationId,versionId,attemptType,lessonId?(solo MODULE),startedAt,clientCreatedAt,answers[{questionId,selectedOption}]}`. Respuesta 201/200(replay) `outcomeDTO` (`dto.go:262`): `{attempt{id,attemptType,score,passed,completedAt}, results[{questionId,selectedOption,correct,correctOption,explanation}], progress, certificate|null, achievements[achievementDTO]}`. `attemptDTO` en `dto.go:246`.
- **Mis asignaciones** `myAssignmentDTO` (`plus_dto.go:118`): `{assignmentId,course{...},dueAt,assignedAt,status PENDING|IN_PROGRESS|COMPLETED|OVERDUE,progressStatus,bestScore,completedAt}`.
- **Mis logros** `myAchievementsDTO` (`plus_dto.go:169`): `{totals{experiencePoints,courseExperiencePoints,moduleExperiencePoints,achievementPoints}, achievements[{id,code,name,description,points,criterionType,criterionThreshold,active,awardedAt|null}]}`.
- **Ruta** `pathDTO` (`plus_dto.go:213`): `{id,title,description,status,rowVersion,publishedAt,courses[{courseId,code,title,position,courseStatus,progressStatus}],progress{status,completedCourses,totalCourses,percent,nextCourseId|null}}`.

### 2.4 XP: dónde se calcula y otorga

- Constantes: `XPPerCorrectFinal = 10` (`T/domain/model.go:43`), `XPPerCorrectModule = 5` (`T/domain/achievements.go:16`).
- FINAL aprobado: `Progress.AfterAttempt` (`T/domain/scoring.go:185-210`): `xp = correctas*10; if xp > p.ExperiencePoints { p.ExperiencePoints = xp }` (conserva el máximo, **nunca baja**, no se suma). Se guarda en `training_progress.experience_points` (`SaveProgress`, `postgres/learn.go:185`). DIAGNOSTIC: 0 XP. FINAL reprobado: 0.
- MODULE aprobado: `attempts.go:114-123` -> `db.SaveModuleResult` (`postgres/achievements.go:120`): upsert con `GREATEST(experience_points)`; `xp = correctas*5` solo si aprobó.
- Insignias: `awardAchievements` (`application/achievements.go:13`, llamada en `attempts.go:137`), puntos = `training_achievements.points`.
- Total visible = curso + módulos + insignias (`postgres/achievements.go:111 XPTotals`, `domain/achievements.go:114 Total()`).
- **No se persiste ni se devuelve "XP ganado por este intento"**: la respuesta solo trae `progress.experiencePoints` (total del curso). El replay (`attempts.go:151-175`) reconstruye `attempt/results` de la BD y `progress/certificate` del estado actual; `achievements` vía `AttemptAchievements` (`source_attempt_id`).

### 2.5 STREAK_DAYS: ya se calcula (en Go, a partir de SQL, en UTC)

- SQL (`T/adapters/postgres/achievements.go:78`): `SELECT DISTINCT (completed_at AT TIME ZONE 'UTC')::date AS d FROM training_attempts WHERE organization_id=$1 AND user_id=$2 AND completed_at IS NOT NULL ORDER BY d DESC LIMIT 60`.
- Go (`T/domain/achievements.go:64-80 StreakDays`): cuenta días UTC consecutivos que terminan en el **día más reciente de la lista**, sin comparar con hoy.
- Evaluación: `AchievementStats` (`postgres/achievements.go:70`) -> `Achievement.Met` (`achievements.go:38`) **solo al registrar un intento nuevo** (`attempts.go:137`), no en lecturas. Catálogo por defecto `STREAK_3` umbral 3 (`achievements.go:60`).
- "Día" = **día calendario UTC** (no zona de la sede ni del usuario). Para Lima (UTC-5) un intento a las 20:00 locales cae al día UTC siguiente.
- Cuenta cualquier intento terminado (DIAGNOSTIC, MODULE o FINAL, aprobado o no). **No** cuenta modo estudio, ver lecciones, ni `PUT progress`.
- Tres limitaciones para usarlo como "racha actual" en el home: (1) no verifica que el último día sea hoy o ayer (una racha vieja de 5 días se devolvería como 5); (2) `LIMIT 60` -> máximo 60, y un umbral 61..100 (permitido por `AchievementUpdate`) jamás se cumple; (3) UTC. `StreakDays` es pura y testeada (`T/domain/plus_test.go:86`): se puede añadir `CurrentStreak(days, today)` sin tocar la evaluación de insignias.

### 2.6 Rutas de aprendizaje (E)

`GET /training/paths?status&pageSize&pageToken` y `GET /training/paths/{pathID}` ya devuelven `progress.nextCourseId` (`plus_dto.go:205-211`; cálculo `T/domain/paths.go:87 ComputePathProgress`): **primer curso PUBLISHED, en orden de `position`, que no está COMPLETED** (no "el siguiente al actual": si el curso 1 quedó pendiente y se aprueba el 2, devuelve el 1). Los aprendices solo ven rutas PUBLISHED (otro `status` -> 403). **No existe consulta curso -> ruta**: el cliente debe listar rutas (≤100 por página) y buscar el `courseId` en `courses[]`. El outcome del intento no menciona rutas.

## 3. Autoría de lecciones y publicación

- Endpoints (ADMIN/SUPER_ADMIN, `T/adapters/http/author.go`): `GET /courses/{id}/draft` (`:62`, crea el borrador clonando la publicada con **IDs nuevos** de lecciones y preguntas: `postgres/author.go:131 cloneContent`), `PUT /courses/{id}/draft` (`:76`), `POST /courses/{id}/publish` (`:124`). **No hay endpoints por lección**: se reemplaza el borrador completo.
- Entrada del `PUT` (`dto.go:64-95`): `draftBody{version{id,versionNumber,passingScore*,rowVersion}, lessons*[draftLessonBody], questions*[draftQuestionBody]}`; `draftLessonBody{id,title,content,displayOrder,videoMediaId*,externalVideo(raw JSON)}` (`dto.go:71`). Salida `draftLessonDTO` (`dto.go:296`).
- Validación (`T/domain/draft.go:23 NormalizeDraft`): `passingScore` 1..100; `rowVersion>=1`; listas obligatorias (nil != vacía); ≤50 lecciones, ≤100 preguntas; lección: UUID válido y único, título 1..200, `content` 0..20000 (multilínea), `videoMediaId` UUID, `externalVideo` YouTube válido y exclusivo con video privado, `displayOrder>0` único (se renumera 1..n); preguntas: 2..6 opciones únicas ≤300, `correctOption` en rango, texto 1..1000, explicación ≤2000, `lessonId` debe existir en el mismo cuerpo. Todos los errores se reúnen en `fields` (`lessons[i].campo`) -> 422 `VALIDATION_ERROR`. Helper de texto: `domain/model.go:204 validText(v,min,max,multiline)`; `indexed()` en `domain/errors.go:60`.
- Guardado (`application/author.go:84 SaveDraft`): autoriza primero, normaliza, bloquea curso (`FOR UPDATE`), exige `rowVersion` igual (409 `TRAINING_DRAFT_CONFLICT`), `ReplaceDraft` (`postgres/author.go:188`) hace `UPDATE row_version+1`, **DELETE de todas las preguntas y lecciones y re-INSERT** (`insertContent` `:158`, INSERT de lección en `:161`), audita `training.draft.saved`.
- Publicación (`author.go:168 Publish` -> `postgres/author.go:227 PublishDraft`): exige ≥1 lección, ≥1 pregunta, videos AVAILABLE (`draft.go:175 ValidateForPublish`; 422 `TRAINING_DRAFT_INCOMPLETE`); archiva la publicada anterior, publica el borrador (`row_version+1`, `published_at`), curso -> PUBLISHED, audita `training.course.published`.
- **Inmutabilidad de lo publicado: solo por convención de aplicación**, sin trigger SQL en `training_lessons/questions/course_versions`. Todas las escrituras de contenido filtran `status='DRAFT'`. Un cambio al contenido de una versión ya publicada implica nuevo borrador -> nueva versión (lecciones con **IDs nuevos**). Consecuencias: (a) todo dato atado a `lesson_id` (p. ej. `training_module_results`, `last_lesson_id`, futuro progreso por lección) queda huérfano para la versión nueva; (b) para que cursos existentes tengan "Lo esencial" hay que editar y re-publicar cada curso (o aceptar un UPDATE de datos que viola la convención).
- Offline: el `etag` del catálogo es el SHA-256 de `json.Marshal([]domain.CourseView)` (`application/offline.go:55`): añadir un campo a `domain.Lesson` cambia el etag una vez (descarga única, inocuo). Se arma con `userID=""` (sin progreso): cualquier dato por usuario no debe entrar en `CourseView` cuando `userID==""`.

## 4. Operaciones (limpieza)

### 4.1 Tablas
- `cleaning_plans(id, organization_id, location_id, area ≤100, task ≤240, instructions, product_name, frequency PER_SHIFT|DAILY|WEEKLY|MONTHLY, due_time TIME NULL, active, audit)` — `V1:572`. **Sin `shift_id`.** Índice `operations_plans_page_idx` (V5:38).
- `cleaning_executions(id, org, location_id, cleaning_plan_id, responsible_user_id, executed_at TIMESTAMPTZ, result COMPLETED|NOT_COMPLETED|OBSERVED, observation, client_operation_id UNIQUE, client_created_at, request_hash[V5], shift_id[V13], audit)` — `V1:595`, `V13:47`. Índices `(org,location_id,executed_at DESC,id DESC)` y `(org,location_id,responsible_user_id,executed_at DESC,id DESC)` (V5:36-37). **No hay índice por `cleaning_plan_id`.**
- `locations.timezone VARCHAR(80) NOT NULL DEFAULT 'America/Lima'` (`V1:59`); `location_shifts(id, org, location_id, name, start_time TIME, end_time TIME, active, row_version)` (`V13:15`); `membership_locations(org, user_id, location_id)` (`V1:105`).
- `bpm_report_timezone(zone)` (V10:6) devuelve la zona o 'America/Lima' si es inválida.

### 4.2 Endpoints y permisos (`O/adapters/http/handler.go:39-43`)
- `GET /cleaning-plans?locationId=<uuid>&pageSize&pageToken&query` (`handler.go:183`; `application/service.go:342`, `postgres/cleaning.go:46`). **Devuelve TODOS los planes de la sede, activos e inactivos** (el DTO trae `active`); orden `lower(area||' '||task)`; sin filtros por frecuencia/fecha/estado. Parámetro de query desconocido -> 400 `INVALID_PAGINATION` (`handler.go:80 query` solo admite `locationId` + paginación + `query`).
- `GET /cleaning-executions?locationId=...` (`handler.go:195`, `cleaning.go:106`): **sin filtro de fechas**; orden `executed_at DESC`; **para WORKER devuelve solo sus propias ejecuciones** (`($7<>'WORKER' OR e.responsible_user_id=$8)`); ADMIN/CHEF ven todas las de la sede.
- DTOs (`O/adapters/http/dto.go:157-208`): `planDTO{id,locationId,area,task,instructions,productName,frequency,dueTime "HH:mm"|"",active}`; `executionDTO{id,locationId,cleaningPlanId,executedAt,result,observation,clientOperationId,clientCreatedAt,shiftId?,area,task,responsibleUserId,responsibleName,shiftName?,createdAt}`.
- `POST /cleaning-executions` (WORKER permitido): `Idempotency-Key` (8..100) + UUID; el plan debe estar activo y ser de esa sede (si no 404); `PER_SHIFT` exige `shiftId` (422 `fields.shiftId`) y `executedAt` dentro del horario del turno en la zona de la sede (422 `SHIFT_OUT_OF_HOURS`) — `application/plus.go:258 checkShift`.
- Permisos (`O/domain/model.go:74-80`, `specs/OPERATIONS.md` tabla): WORKER consulta planes de **sedes asignadas** (`membership_locations`; `postgres/repository.go:92 Location` -> 403 `ACCESS_DENIED` si no está asignado; ADMIN/SUPER_ADMIN ven todas las sedes activas) y ejecuta limpieza; no crea planes (`CanConfigure` = ADMIN/SUPER_ADMIN).
- Zona horaria: nunca la del dispositivo; `Location()` devuelve `locations.timezone` y se usa `time.LoadLocation` (tzdata embebido en `shared/domain/shiftwindow`). La zona ya es visible al cliente (organizations: `timezone` en sedes; OpenAPI `Location` ~4021).
- **PER_SHIFT y turnos**: `PER_SHIFT` solo significa "se hace en cada turno"; el plan no apunta a un turno. La ejecución lleva `shift_id`. Ventana del turno: `shared/domain/shiftwindow` (`New`, `Select(now, zone)` -> inicio/fin/estado OPEN|CLOSED|UPCOMING, fin excluyente, cruza medianoche). `GET /shifts?locationId=&status=ACTIVE` ya expone `openNow`.
- **Calendario por frecuencia ya existe en checklists** y es el patrón a reutilizar: `internal/features/checklists/domain/assignment.go:47 PeriodAt` (DAILY día local; WEEKLY semana que empieza lunes `(weekday+6)%7`; MONTHLY mes calendario; todo en la zona de la sede; periodo semiabierto) y `checklist_assignments` ya devuelven `periodStartsAt/periodEndsAt/run/shiftState` (`checklists/adapters/http/assignments.go:34-42`). Hoy limpieza no tiene equivalente.
- Spec desactualizada: `OPERATIONS.md` dice "ni introduce turnos que todavía no están implementados" (sección limpieza) pero V13 sí los implementó (sección final del mismo archivo).

## 5. Convenciones de código

- **Capas** (`AGENTS.md`, `specs/ARCHITECTURE.md`): `domain` (reglas puras, sin HTTP/pgx), `application` (casos de uso + puertos `Store`/`UnitOfWork`, `Within(ctx, fn)` = transacción), `adapters/postgres` (pgx, SQL a mano, `store{tx}`), `adapters/http` (chi; DTO separados de entidades). Composición manual en `internal/core/app/container.go`; `module.go` por feature. Ej.: `T/application/ports.go` (interfaces), `T/adapters/postgres/repository.go:80 Actor`.
- **Tenant/rol**: el tenant sale de la ruta pero se **re-autoriza contra la membresía** en cada caso de uso: training `s.actor(ctx, db, user, org, allowed)` (`T/application/service.go:29`; predicados `learner/persister/author` `:40-42`); operations `roleSite(org, site, allowed)` (`O/application/service.go:36`) + `db.Location`. UID: `request.VerifiedUID` (`T/adapters/http/handler.go:115 identity`). Toda consulta lleva `organization_id`; recurso ajeno -> 404; no miembro -> 403 `ACCESS_DENIED`. La autorización va **antes** de validar formato.
- **Errores**: dominio expone errores comparables (`T/domain/errors.go`, `ValidationError{Fields}`); el handler los traduce en un único `fail()` (`T/adapters/http/handler.go:58-103`, `plus.go:44 failPlus`) a `response.Error(w, status, requestID, APIError{Code,Message,Fields})`; 500 genérico sin detalles (`response.InternalError`). Catálogo en `specs/API_ERRORS.md`. Validación = 422 `VALIDATION_ERROR` con `fields`; formato/paginación/campos desconocidos = 400 `INVALID_REQUEST`.
- **JSON estricto**: `request.DecodeStrictJSON(w,r,&body,maxBody)` rechaza campos desconocidos -> toda extensión de **entrada** debe declararse en el DTO; las **salidas** nuevas son opcionales y el KMP las ignora (`ignoreUnknownKeys = true` en `Frontend/composeApp/.../core/network/HttpClientFactory.kt:24` y `training/data/RemoteTrainingRepository.kt:305`).
- **Idempotencia**: training = `id` + `clientOperationId` UUID del cliente + `request_hash` SHA-256 canónico; mismo contenido -> 200 replay, distinto -> 409 `IDEMPOTENCY_CONFLICT` (`attempts.go:30-40,151-175`). Operations = cabecera `Idempotency-Key` + tabla `idempotency_records` (24 h) vía `write[T]` (`O/application/service.go:62`, `postgres/repository.go Replay/Remember`) más hash propio de la ejecución. Bloqueos: `pg_advisory_xact_lock` (`postgres/learn.go:171 LockLearner`, `operations Lock`).
- **Auditoría**: `Store.Audit(ctx, org, user, action, entityType, entityID)` -> `audit_events` con `metadata '{}'`, nunca contenido/respuestas (`T/adapters/postgres/repository.go:103`; ej. `"training.attempt."+type`, `attempts.go:143`).
- **Paginación**: `pagination.Open/Close` con cursor opaco ligado a `scope` (usuario, org, recurso, filtros) — `shared/application/pagination`; `listQuery` (`T/adapters/http/plus.go:79`) y `O/application/service.go:278 list[T]`. Si se añade un filtro a un listado hay que meterlo en el `scope` del cursor.
- **Migraciones aditivas** con `RequireSchema` que consulta las columnas nuevas (`T/adapters/postgres/repository.go:30`); `NOT VALID` para CHECK sobre datos viejos.
- **Pruebas** (ejemplo por patrón):
  - Dominio, tablas/casos: `T/domain/domain_test.go` (`TestScorePercentRoundsHalfUp`, `TestGradeAttemptPassingBoundaryIsInclusive`), `T/domain/plus_test.go:86` (racha), `O/domain/rules_test.go`.
  - HTTP con repositorio en memoria (`fakeRepo` implementa `application.Store`): `T/adapters/http/fake_test.go`, `fake_plus_test.go`, `handler_test.go`, `plus_test.go` (`newEnv(t)`, `e.do(method,path,user,body)`). **Cada método nuevo del `Store` debe implementarse también en los fakes** o no compila.
  - PostgreSQL real aislado: `T/adapters/postgres/repository_test.go:30 database(t)` (+`seed`), `plus_test.go:224 TestAchievementsAreAwardedOnceAndModuleAttemptsStayScoped`, `O/adapters/postgres/plus_test.go:19`.
  - Aplicación con fakes: `O/application/service_test.go`.
- **Contrato**: actualizar `internal/core/httpserver/docs/openapi.yaml` y la spec (`specs/TRAINING.md`, `OPERATIONS.md`) en el mismo cambio (AGENTS: documentación en español).
- **Proxy web (no KMP)**: `Software/Frontend/web/training-proxy.mjs` **valida y proyecta solo campos conocidos**: `progress()` (`:63`), vista de curso (`:94-112`), borrador (`:117-135`, `shape(lesson,[...],['externalVideo'])` `:233`), `PUT progress` `exact(value,['lastLessonId'])` (`:212`), outcome de intento (`:512`), `me/achievements` (`:563`). Cualquier campo nuevo se **descarta en la web** hasta actualizar ese proxy (y su `training-proxy.test.mjs`); el KMP no se ve afectado.

## 6. Diseño mínimo recomendado (A-F)

Principios: campos nuevos **opcionales y aditivos** en respuestas existentes; entradas nuevas opcionales; nada se renombra ni elimina; una única migración **V15** (aditiva, transaccional, idempotente donde sea posible) para A, C y D; B, E y F sin cambio de esquema (salvo un índice opcional en F).

### A. Progreso por lección y % por curso
- **Hoy**: solo `training_progress.last_lesson_id` y `training_module_results` (lecciones con intento MODULE).
- **Migración V15**: `CREATE TABLE training_lesson_progress(organization_id, user_id, course_id, lesson_id, completed_at TIMESTAMPTZ NOT NULL DEFAULT now(), source VARCHAR(20) CHECK (source IN ('VIEWED','MODULE_PASSED')), audit cols, PRIMARY KEY (organization_id,user_id,lesson_id), FK (org,user)->organization_memberships, FK (org,lesson_id)->training_lessons, FK (org,course_id)->training_courses)` + índice `(org,user_id,course_id)`.
- **Escritura**: extender `PUT /courses/{id}/progress` (`progressBody`, `dto.go:26`) con `completedLessonId` opcional (UUID, misma validación `LessonInVersion`; idempotente por PK `ON CONFLICT DO NOTHING`; `lastLessonId` pasa a ser opcional **si** viene `completedLessonId`, pero hoy es obligatorio: mantener obligatorio para compatibilidad). Además, un intento MODULE aprobado marca esa lección `MODULE_PASSED` dentro de `SubmitAttempt` (junto a `SaveModuleResult`, `attempts.go:114-123`).
- **Lectura**: en `progressDTO` añadir `completedLessonIds: string[]`, `completedLessons: int`, `totalLessons: int`, `percent: int` (calculado en servidor contando solo lecciones de la versión PUBLISHED). Rellenar en `postgres/learn.go:87-95` (CourseView con `userID != ""`), **no** cuando `userID==""` (etag offline). `me/assignments` podría añadir `percent` por curso con un LEFT JOIN agregado (`postgres/assignments.go:193`).
- **Riesgos**: (1) nuevas versiones publicadas regeneran IDs de lección -> el % de una versión nueva parte de 0 aunque el curso estuviera completado; mitigación: cuando `progress.status=COMPLETED` mostrar 100 % o mapear lecciones por `display_order`/columna `origin_lesson_id` (más cambios en `cloneContent`); (2) `PUT progress` hoy exige lección en versión publicada: reusar; (3) `PUT` estricto en el proxy web (`exact ['lastLessonId']`); (4) `SaveProgress` retrocede `lastLessonId` si se llama con otra (ya es así). (5) alternativa sin tabla: derivar de `training_module_results.passed` (sin marcar "vista"): insuficiente para cursos sin preguntas por lección.

### B. Racha de aprendizaje actual (home)
- **Hallazgo**: ya existe cálculo de días consecutivos (2.5), solo para insignias, en UTC y sin "hoy".
- **Propuesta sin migración**: nuevo método de `Store` `LearningDays(ctx, org, user, tz, limit)` o reutilizar `AchievementStats` y añadir `domain.CurrentStreak(days []time.Time, today time.Time) (current, longest int, lastDay *time.Time)`: racha vigente = consecutivos terminando hoy **o ayer** (si el último día es anterior a ayer -> 0). Exponer en `GET /me/achievements` (`myAchievementsDTO`, `plus_dto.go:169`): `"streak": {"currentDays": 4, "longestDays": 9, "lastActivityDate": "2026-11-19", "activeToday": true, "timezone": "America/Lima"}` (aditivo). `MyAchievements` ya está permitido a ADMIN/CHEF/WORKER con membresía.
- **Decisión de zona**: el usuario no tiene zona; opciones: (a) misma UTC que el criterio de insignias (consistente, pero Lima cambia de día a las 19:00); (b) zona de la sede del usuario (`membership_locations` -> `locations.timezone`, con `bpm_report_timezone` y fallback `America/Lima`) o query opcional; recomendado: calcular con zona de la primera sede activa asignada o 'America/Lima', y **dejar sin tocar** la evaluación de STREAK_3 (documentar la diferencia) o migrar ambas a la vez.
- **Qué cuenta**: hoy solo intentos. Si se quiere "actividad de aprendizaje" (ver lección), se puede unir `training_lesson_progress.completed_at` (A) al conjunto de días: `UNION` de `training_attempts.completed_at` y `training_lesson_progress.completed_at`. Sin A, abrir lecciones no suma.
- **Riesgos**: `LIMIT 60` (subir a 400 o calcular con SQL de huecos); cambio de semántica de "día" si se alinea con insignias (podría otorgar/denegar `STREAK_3` distinto a lo ya otorgado; el otorgamiento es permanente); el web proxy (`:563`) descarta `streak`.

### C. "Lo esencial" (2-3 puntos clave por lección, autoría ADMIN)
- **Migración V15**: `ALTER TABLE training_lessons ADD COLUMN key_points JSONB NOT NULL DEFAULT '[]'::jsonb, ADD CONSTRAINT training_lessons_key_points_check CHECK (jsonb_typeof(key_points)='array' AND jsonb_array_length(key_points) <= 5) NOT VALID;` (límite de longitud por ítem se valida en dominio).
- **Dominio** (`domain.Lesson`, `model.go:97`): `KeyPoints []string`; `NormalizeDraft` (`draft.go:56-88`): hasta 5 (producto pide 2-3: validar ≤5 y avisar en UI), cada uno trim, 1..200 caracteres con `validText(...,false)`, sin duplicados; error en `lessons[i].keyPoints`.
- **DTO entrada**: `draftLessonBody.KeyPoints *[]string json:"keyPoints"` (`dto.go:71`). **Riesgo de compatibilidad clave**: `PUT draft` borra y reinserta todas las lecciones (`postgres/author.go:196-201`); un admin con app vieja que no envía `keyPoints` borraría los puntos existentes. Mitigación: si el campo es `nil` (ausente) conservar los puntos previos de esa lección (mismo `id`) antes del DELETE; si es `[]` explícito, vaciar.
- **DTO salida**: `draftLessonDTO` (`dto.go:296`), `learnLessonDTO` (`dto.go:183`), `offlineLessonDTO` (`plus_dto.go:245`) con `keyPoints: []string` (siempre array, vacío si no hay). Tocar SQL: `postgres/learn.go:37` (CourseView), `postgres/author.go:68` (`content`), `:161` (insert), `cloneContent` copia el struct (automático). El etag offline cambia una vez.
- **Inmutabilidad**: cursos ya publicados no tendrán puntos hasta nuevo borrador+publicación (ver sección 3). No hacer UPDATE directo sobre versiones PUBLISHED.
- **Proxy web**: añadir `keyPoints` a `shape(lesson,...)` (`training-proxy.mjs:233`) y a la proyección de lecciones (`:98-103`, `:120-125`).

### D. XP ganado por un intento
- **Definición**: `xpEarned = deltaCurso + deltaMódulo + puntosDeInsignias`, donde deltaCurso = `max(0, nuevoXPcurso - anterior)` (FINAL aprobado, `scoring.go:205`), deltaMódulo = `max(0, xpMódulo - xpMóduloPrevio)` (MODULE aprobado; hoy `SaveModuleResult` no devuelve el valor previo: leerlo antes o usar CTE con `RETURNING`), insignias = suma de `points` de `awarded` (`attempts.go:137`).
- **Respuesta**: en `attemptDTO` (`dto.go:246`) añadir `xpEarned int` y opcional `xpBreakdown{course,module,achievements}`; no cambia `progress.experiencePoints` (sigue siendo total del curso). Para un FINAL repetido con la misma nota el valor es 0 (por diseño de "conservar el máximo"): la UI debe tolerarlo.
- **Replay**: no se puede recomputar desde el estado actual -> persistir. V15: `ALTER TABLE training_attempts ADD COLUMN xp_awarded INTEGER` (NULL en intentos históricos; guarda curso+módulo). `InsertAttempt` (`attempts.go:103`) ocurre antes de calcular el progreso (`:106-123`): reordenar o `UPDATE training_attempts SET xp_awarded` al final (la tabla no tiene trigger de inmutabilidad). En replay (`attempts.go:151`) devolver `xp_awarded + sum(points de AttemptAchievements)`; NULL -> omitir/`null`.
- **Riesgos**: la huella `request_hash` solo cubre la entrada (no cambia); insignias con puntos editados después alteran el replay (aceptable o guardar snapshot); `RequireSchema` debe verificar `xp_awarded`; fakes de tests; proxy web (`:512`) descarta `xpEarned`.

### E. Siguiente curso tras aprobar
- **Confirmado**: la API ya trae `nextCourseId` (2.6) en `GET /paths` y `GET /paths/{id}`; no hay relación curso->ruta.
- **Mínimo sin backend**: tras un FINAL aprobado, el cliente llama `GET /paths?status=PUBLISHED&pageSize=100`, toma la(s) ruta(s) cuyo `courses[]` incluye el `courseId` y usa `progress.nextCourseId` (≠ el curso actual; puede ser un curso anterior pendiente). Si no hay ruta, caer a `GET /me/assignments` (primer curso `PENDING`/`OVERDUE`).
- **Opcional (aditivo)**: en `outcomeDTO` (`dto.go:262`) añadir `nextCourse: {pathId, pathTitle, courseId, courseTitle}|null`, calculado en `SubmitAttempt` con una consulta sobre `learning_path_courses` (índice `learning_path_courses_course_idx`) reutilizando `ComputePathProgress`; elegir la ruta publicada con curso aprobado más reciente. Riesgo: varias rutas contienen el curso (definir criterio y devolver lista `nextCourses` si hay ambigüedad); en replay reflejaría el estado actual.

### F. "Tareas de hoy" del WORKER (limpieza)
- **Lo existente no alcanza**: planes sin filtro de activos; ejecuciones sin fechas y, para WORKER, solo las propias; el cliente tendría que paginar y calcular periodos por su cuenta (y conocer la zona de la sede).
- **Propuesta (extender, sin migración obligatoria)**: `GET /cleaning-plans?locationId=...&view=today` (parámetro nuevo opcional que `handler.go:80 query` hoy rechazaría -> admitirlo explícitamente) que devuelve solo planes `active=true` y añade a cada `planDTO` el objeto opcional `today`:
  `{"periodStartsAt":"...Z","periodEndsAt":"...Z","timezone":"America/Lima","done":true,"doneByMe":true,"lastExecutionId":"uuid|null","lastResult":"COMPLETED|OBSERVED|NOT_COMPLETED|null","lastExecutedAt":"...|null","overdue":false,"shift":{"id":"uuid","name":"Mañana","state":"OPEN"}|null}`.
  Si no se pide `view=today`, el DTO queda idéntico (old frontend OK).
- **Cálculo en servidor** (zona = `locations.timezone`, vía `db.Location`): periodo por frecuencia extrayendo/compartiendo la lógica de `checklists/domain/assignment.go:47 PeriodAt` a un paquete puro en `shared/domain` (DAILY día local, WEEKLY desde el lunes, MONTHLY mes). `done` = existe ejecución del plan en `[periodStartsAt, periodEndsAt)` con `result IN ('COMPLETED','OBSERVED')` (decisión de producto; `NOT_COMPLETED` no cuenta). `overdue` = no `done` y hora local actual > `dueTime` (solo DAILY, o si el periodo cierra). Un `COUNT/EXISTS` por lote de planes (`WHERE cleaning_plan_id = ANY($ids) AND executed_at >= $start AND executed_at < $end`).
- **PER_SHIFT**: sin `shift_id` en el plan; usar turnos activos de la sede (`location_shifts`, `shiftwindow.Select(now, zone)`): `today.shift` = turno abierto (o el próximo); `done` = ejecución con `shift_id` = ese turno dentro de su ventana. Sin turnos activos, un plan PER_SHIFT no se puede ejecutar (`checkShift` exige `shiftId`, 422): devolver `today:null` o `done:false` con `shift:null`.
- **WORKER y visibilidad**: la spec dice que el WORKER ve solo sus ejecuciones; para que `done` refleje trabajo de un compañero hace falta una consulta no filtrada por responsable. Recomendado: `done` global de la sede (booleano), `doneByMe`/`lastExecution*` solo con las propias; no exponer nombre de otros. Es decisión de producto/espec (`OPERATIONS.md` tabla de permisos).
- **Cursor**: añadir `view` al `scope` de paginación (`service.go:278`) para que el cursor no valga entre vistas.
- **Índice opcional V15**: `CREATE INDEX operations_executions_plan_idx ON cleaning_executions (organization_id, cleaning_plan_id, executed_at DESC)`.
- **Riesgos**: DST/zonas inválidas (usar `bpm_report_timezone`/fallback); un worker con varias sedes necesita elegir `locationId` (viene de `membership_locations`, no hay endpoint "mis sedes" específico de operaciones: usar workspace/locations); `due_time` es hora local sin fecha (`left(due_time::text,5)`); un plan `WEEKLY` "hecho esta semana" no es una tarea "de hoy" (definir si se muestra); `OPERATIONS.md` y OpenAPI (`OperationPlan` ~1651) deben actualizarse; proxy de operaciones web (si existe en `Frontend/web`) también proyecta campos.

### Resumen de V15 propuesta (un solo archivo, aditiva)
`training_lesson_progress` (A) + `training_lessons.key_points` (C) + `training_attempts.xp_awarded` (D) + índice `cleaning_executions(plan)` (F). B y E sin cambios de esquema. Orden de entrega recomendado: B -> E (sin migración) -> C -> A -> D -> F, aplicando V15 en Railway (con `scripts/aplicar-migraciones.ps1`) **antes** de mergear a `main`, añadiéndola a `RequireSchema`, a la lista de `repository_test.go:56`, al script `.ps1` y a `specs/MIGRATIONS.md`/`AGENTS.md`.
