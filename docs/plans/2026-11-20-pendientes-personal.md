# Plan: cerrar los pendientes del rol Personal (backend + frontend)

Hechos: `docs/plans/hechos-pendientes-personal.md` (sección 6 = diseño base que este plan adopta).
Frontend: `/Users/helcorp/Downloads/Proyectos/BPM/Software/Frontend` (plan previo en `docs/plans/2026-11-20-rediseno-personal.md`).
Ramas: backend `feat/pendientes-personal`, frontend `feat/pendientes-personal`. Sin commit ni push hasta el cierre.

## Decisiones (no reabrir)

- Todo **aditivo y retrocompatible**: campos opcionales nuevos; la app vieja sigue funcionando.
- **Una migración V15** aditiva: `training_lesson_progress`, `training_lessons.key_points`, `training_attempts.xp_awarded`,
  índice `cleaning_executions(organization_id, cleaning_plan_id, executed_at DESC)`. Agregarla a `RequireSchema`,
  a la lista de `internal/features/training/adapters/postgres/repository_test.go:56` (y equivalentes de operations si existen),
  a `scripts/aplicar-migraciones.ps1`, `specs/MIGRATIONS.md`, `AGENTS.md`.
- **A. Progreso por lección**: `PUT /courses/{id}/progress` acepta `completedLessonId` opcional (además de `lastLessonId`, que sigue obligatorio).
  Intento MODULE aprobado marca `MODULE_PASSED`. `progress` agrega `completedLessonIds`, `completedLessons`, `totalLessons`, `percent`
  (si `status=COMPLETED` → `percent=100` y todas como completadas). `GET /me/assignments` agrega `percent` por curso. Nunca meter datos de usuario en el CourseView offline (`userID==""`).
- **B. Racha**: `GET /me/achievements` agrega `streak {currentDays, longestDays, lastActivityDate, activeToday, timezone}`.
  Días = UNION de `training_attempts.completed_at` y `training_lesson_progress.completed_at`, en la zona de la primera sede activa asignada
  (si es ADMIN sin sedes o no hay, `America/Lima`). Racha vigente si termina hoy o ayer. Ventana 400 días. **No** cambiar la evaluación de la insignia STREAK.
- **C. Lo esencial**: `key_points JSONB` (array ≤ 5, cada ítem 1..200 chars, trim, sin duplicados). Entrada `keyPoints` en el borrador:
  ausente = conservar los previos de esa lección; `[]` = vaciar. Salida `keyPoints` (siempre array) en lección de alumno, borrador y catálogo offline.
- **D. XP por intento**: persistir `xp_awarded` (curso + módulo); `attempt.xpEarned` = xp_awarded + puntos de insignias otorgadas; en replay igual; NULL histórico → omitir.
- **E. Siguiente curso**: `outcome.nextCourse {pathId, pathTitle, courseId, courseTitle} | null` tras FINAL aprobado: ruta PUBLISHED que contiene el curso, su `nextCourseId` ≠ curso actual; si varias, la publicada más recientemente; si ninguna, el primer `PENDING/OVERDUE` de mis asignaciones; si nada, null.
- **F. Tareas de hoy**: `GET /cleaning-plans?locationId=..&view=today` solo activos + `today {periodStartsAt, periodEndsAt, timezone, done, doneByMe, lastExecutionId, lastResult, lastExecutedAt, overdue, shift}`.
  Periodo por frecuencia en zona de la sede (reutilizar `checklists/domain/assignment.go PeriodAt`, extrayendo a lógica pura compartida si hace falta).
  `done` = ejecución del plan en el periodo con resultado COMPLETED u OBSERVED de **cualquier persona de la sede**; `doneByMe` y `last*` solo con las propias (no exponer nombres ajenos).
  `overdue` = no done y hora local > `dueTime` (DAILY/PER_SHIFT con dueTime). PER_SHIFT: turno abierto actual (o próximo) de la sede; sin turnos → `today.shift=null, done=false`.
  `view` entra al scope del cursor. Sin `view` la respuesta es idéntica a hoy.
- Actualizar specs (`TRAINING.md`, `OPERATIONS.md`) y OpenAPI si existe archivo.

## Paquetes

- **B1 (backend)**: V15 + A + C (+ autoría: conservar keyPoints ausentes) + pruebas de dominio y handler.
- **B2 (backend)**: B + D + E + pruebas.
- **B3 (backend)**: F + pruebas.
- **F1 (frontend datos)**: DTOs/dominio/repositorios para A-F (campos nuevos al final con default), `completedLessonId` al avanzar
  de lección (al pulsar «Siguiente lección» / «Terminar lecciones» marcar la lección actual), `view=today` en operaciones,
  proxy web `web/training-proxy.mjs` (y el de operaciones si proyecta campos) + sus pruebas node; pruebas Kotlin de DTO.
- **F2 (frontend UI)**: Hoy con racha real (`BpmStreak…` o ampliar `BpmRewardSummary`) y tareas de hoy (plan con done/overdue);
  temario con `completedLessonIds` (reemplaza la aproximación de `lessonStepStates` cuando el dato existe) y anillos con `percent`;
  «Lo esencial» en la lección; campo «Lo esencial» en la autoría de lecciones (admin); resultado con «+N XP» y botón «Siguiente curso: X»;
  Tareas «Por hacer» con `today` (chip Hecho/Vencida/hasta HH:mm desde el servidor, sin depender de la hora del cliente).
- **V (verificación)**: backend `gofmt -l`, `go vet ./...`, `go test ./...`, `go test -race ./...`, `git diff --check` (+ pruebas PG con `TEST_DATABASE_URL` si hay Postgres local);
  frontend `jvmTest`, `wasmJsBrowserDistribution`, `assembleDebug`, `node --test web/*.test.mjs`.
- **Cierre (Opus)**: aplicar V15 en producción, commit + merge a main + push de ambos repos, prueba de humo contra la API desplegada.

## Estado B1

Hecho (V15 + A + C), sin commit ni push.

**Archivos.** `migraciones/V15__training_lesson_progress.sql` (nuevo); `training/adapters/postgres/{repository,learn,author,assignments}.go`;
`training/application/{ports,learn,attempts,author}.go`; `training/domain/{model,scoring,draft,assignments}.go`;
`training/adapters/http/{dto,learn,author,plus_dto}.go`; fakes `fake_test.go`/`fake_plus_test.go`; pruebas nuevas
`domain/lesson_progress_test.go`, `adapters/http/lesson_progress_test.go`, `adapters/postgres/lesson_progress_test.go`;
`repository_test.go` (lista con V15 y firma de `SaveProgress`); `scripts/aplicar-migraciones.ps1`, `specs/MIGRATIONS.md`, `specs/TRAINING.md`,
`AGENTS.md`, `internal/core/httpserver/docs/openapi.yaml`.

**API añadida (todo opcional/aditivo).**
- `PUT /courses/{id}/progress`: `completedLessonId` opcional (`lastLessonId` sigue obligatorio); 404 `TRAINING_LESSON_NOT_FOUND` si no es de la versión publicada, 422 `fields.completedLessonId` si no es UUID.
- `progress` (curso, PUT progress, outcome de intento y replay): `completedLessonIds`, `completedLessons`, `totalLessons`, `percent`. COMPLETED → 100 y todas completadas. No se rellena con `userID==""` (offline).
- `GET /me/assignments`: `percent` por curso.
- Intento MODULE aprobado marca la lección `MODULE_PASSED` (un `VIEWED` previo se mejora; reprobado no marca).
- `keyPoints`: entrada en `PUT draft` (ausente/null = conservar los previos por `id` de lección; `[]` = vaciar; ≤5, 1..200 chars, una línea, sin duplicados case-insensitive, error en `lessons[i].keyPoints`); salida siempre arreglo en lección de alumno, borrador y catálogo offline.
- Store nuevo: `LessonProgress(org,user,versionID)` y `CompleteLesson(org,user,course,lesson,source)`; `Service.SaveProgress` recibe `completedLessonID`.

**Decisiones/desviaciones.**
- "Ausente = conservar" se resuelve en la capa de aplicación (`keepKeyPoints`, antes de `ReplaceDraft`), no en SQL; así los fakes lo reproducen.
- `RequireSchema` de training exige V15 completa (tabla, `key_points`, `xp_awarded` e índice `cleaning_executions_plan_idx`); operations no se tocó (el índice es solo de rendimiento).
- Los CHECK de V15 (`key_points` ≤5, `xp_awarded >= 0`) se crean validados (las filas existentes cumplen), no `NOT VALID`.
- `CompleteLesson` no cambia `completed_at` al repetirse (la racha de B usa esa fecha).
- El `etag` offline cambia una vez (campo `keyPoints`); `Progress` offline queda con ceros.
- El proxy web aún descarta los campos nuevos (paquete F1).

**Pruebas PG.** Postgres 18 temporal con `initdb` en el scratchpad (puerto 55432, socket en `/private/tmp/b1sock`),
base `bpm_test`, `TEST_DATABASE_URL=postgres://postgres@127.0.0.1:55432/bpm_test?sslmode=disable`; `go test -p 1 ./...` OK
(en paralelo fallan por una carrera preexistente de `CREATE EXTENSION` entre paquetes que comparten la base). Servidor detenido y borrado al terminar.
Resultados: `gofmt -l .` vacío, `go vet ./...` OK, `go test ./...` OK (con y sin DB), `go test -race` de training OK, `git diff --check` OK.

## Estado B3

Hecho (F: tareas de hoy), sin commit ni push. Sin migraciones nuevas (usa el índice `cleaning_executions_plan_idx` de V15, solo rendimiento).

**Archivos.** Nuevo `internal/shared/domain/calendarperiod/{calendarperiod,calendarperiod_test}.go` (cálculo puro de período DAILY/WEEKLY/MONTHLY,
extraído de `checklists/domain/assignment.go`, que ahora lo invoca sin cambiar su comportamiento; pruebas de checklists intactas).
Operations: `domain/{today,today_test}.go`; `application/{ports,service}.go` (`ActivePlans`, `PlanActivity`, `TodayPlans`; `list` acepta ámbito extra de cursor);
`adapters/postgres/{cleaning,today_test}.go`; `adapters/http/{handler,dto,today_test}.go`; `specs/OPERATIONS.md`; `openapi.yaml` (solo `cleaning-plans`, `OperationPlan*`).

**API.** `GET /cleaning-plans?locationId=&view=today` (`view` único valor `today`; otro/vacío/repetido = 400 `INVALID_PAGINATION`). Sin `view` la respuesta es idéntica
(prueba con la respuesta literal). `view` entra al ámbito del cursor como `view=today`; los cursores sin vista conservan su ámbito anterior.

**Decisiones/desviaciones.**
- `last*` = última ejecución **propia dentro del período** (puede ser `NOT_COMPLETED`), no la última histórica.
- PER_SHIFT con varios turnos: el abierto; si no, el próximo; si ya cerraron todos, el que cerró más tarde (`state` `OPEN|UPCOMING|CLOSED`). `done` exige ejecución con ese `shift_id` dentro de su ventana.
  Sin turnos activos: `shift=null`, `done=false`, `overdue=false`, período = día local (no se consulta actividad).
- `overdue` PER_SHIFT con turno `UPCOMING` es `false` (refinamiento: un turno que no empezó no puede estar vencido); comparación con segundos y estrictamente mayor que `dueTime`, en hora local (no depende de la duración del día, DST-safe).
- Zona de sede inválida: fallback `America/Lima` (también en `timezone` de la respuesta).
- Actividad agregada en SQL (`unnest` de planes+ventanas, `bool_or`/`array_agg ... FILTER`), una sola consulta por página; `ActivePlans` comparte SQL con `Plans` (`NOT $7 OR active`).
- OpenAPI: se añadió `PER_SHIFT` al enum de frecuencia de `OperationPlan`/`OperationPlanInput` (estaba desactualizado) y el esquema `OperationPlanToday`. El proxy web aún no proyecta `today` (paquete F1).

**Pruebas.** Dominio (períodos DAILY/WEEKLY/MONTHLY, frontera de zona Lima/Tokio, DST New York 23/25 h, turnos abierto/próximo/cerrado/cruce de medianoche/sin turnos,
overdue, compañero vs yo), handler (validación de `view`, respuesta por omisión byte a byte, solo activos, un solo lote de actividad, cursor por vista, 403) y PostgreSQL real
(`TestTodayPlansPostgres`: compañero vs propio, NOT_COMPLETED, fuera de período, turno, roles/sedes/tenant, cursor).
Postgres 18 temporal en `/private/tmp/b3pg` puerto 55434, base `bpm_test`; `go test -p 1 ./internal/features/operations/... ./internal/features/checklists/...` OK (y con `-race` en operations). Servidor detenido y borrado.

## Estado B2

Hecho (B + D + E), sin commit ni push, sin migraciones nuevas (usa V15).

**Archivos.** `training/domain/{streak,nextcourse,achievements}.go` (+ `streak_test.go`); `training/application/{attempts,achievements,nextcourse,ports,ports_plus}.go`;
`training/adapters/postgres/{achievements,paths,learn}.go` (+ `b2_test.go`); `training/adapters/http/{dto,plus_dto}.go`, fakes `fake_plus_test.go` (+ `b2_test.go`);
`internal/core/httpserver/docs/openapi.yaml` (solo Academia: intento y `me/achievements`); `specs/TRAINING.md` (sección B2).

**API añadida (aditiva).**
- `GET /me/achievements`: `streak {currentDays, longestDays, lastActivityDate|null, activeToday, timezone}`. Unión de `training_attempts.completed_at` y
  `training_lesson_progress.completed_at`, zona de la primera sede activa asignada (orden `membership_locations.created_at`, nombre, id; sin sede o zona inválida: `America/Lima`),
  ventana de 400 días, vigente si termina hoy o ayer. `domain.CurrentStreak` es pura (recibe instantes y `*time.Location`). `StreakDays`/STREAK_3 no se tocaron.
- `attempt.xpEarned`: `AttemptXP` (FINAL: diferencia de XP de curso; MODULE aprobado: diferencia con el mejor XP del módulo; resto 0) se guarda en `xp_awarded` y se suma a los puntos de insignias otorgadas;
  replay igual (puntos de `AttemptAchievements`); `xp_awarded` NULL -> se omite (`omitempty` sobre puntero).
- `outcome.nextCourse` (siempre presente, `null` si no aplica): `domain.SelectNextCourse` sobre rutas PUBLISHED que contienen el curso (nuevo `PublishedPathsWithCourse`, más reciente primero) y,
  si ninguna propone, la primera asignación PENDING/OVERDUE de `MyAssignments` (hasta 100) distinta del curso (`pathId`/`pathTitle` null).

**Decisiones/desviaciones.**
- En `SubmitAttempt` se calcula el progreso y el XP antes de `InsertAttempt` (para guardar `xp_awarded` en el mismo INSERT, sin UPDATE); nuevo store `ModuleXP`, `LearningActivity`, `UserTimezone`.
- En replay de un FINAL aprobado `nextCourse` se recalcula con el estado actual (no se persiste); `xpEarned` en replay usa los puntos vigentes de las insignias del catálogo.
- "Primer" PENDING/OVERDUE sigue el orden de `me/assignments` (más recientemente asignada primero).
- La zona se resuelve en la capa de aplicación (`time/tzdata` embebido).

**Pruebas.** Dominio (tabla de racha con frontera de zona y DST, `AttemptXP`, `SelectNextCourse`), HTTP con fakes y PostgreSQL real (Postgres temporal, puerto 55433, base `bpm_test`, `go test -p 1 ./internal/features/training/...` OK; servidor detenido y borrado).

## Estado F1

Hecho (capa de datos del frontend), sin commit ni push. Rama `feat/pendientes-personal` del frontend.

**Dominio/DTO (campos nuevos al final con default; cachés viejas siguen decodificando).** `TrainingProgress` (+`completedLessonIds`, `completedLessons`, `totalLessons`, `percent`; helper `withCompletedLesson`), `TrainingLesson.keyPoints` y `DraftLesson.keyPoints` (lector, catálogo offline y borrador), `MyAssignment.percent`, `MyAchievements.streak: LearningStreak?`, `AttemptSummary.xpEarned: Int?`, `AttemptOutcome.nextCourse: NextCourse?` (`pathId`/`pathTitle` nulos si no viene de ruta). `AttemptRecordDto` guarda `xpEarned`.
**Avance.** `saveProgress(org, course, lessonId, completedLessonId = null)`; el JSON solo lleva `completedLessonId` si no es nulo. `TrainingViewModel.nextLesson()` envía `completedLessonId` = lección actual junto con `lastLessonId` = siguiente; nuevo `finishLessons()` (usado por «Terminar lecciones» en `LessonFlow.kt`, único cambio en un composable) marca la actual y vuelve al temario. Respuesta del servidor actualiza `course.progress`; con servidor anterior se marca localmente. Sin conexión (QUEUED) el avance local conserva las lecciones terminadas y se fusiona al leer el curso.
**Borrador.** `saveDraft` siempre envía `keyPoints` (recortados, sin vacíos); `[]` vacía. Constantes `TrainingLimits.KEY_POINTS_MAX=5`, `KEY_POINT_MAX=200`; la validación de UI queda para F2.
**Operaciones.** `CleaningPlan.today: CleaningPlanToday?` (+`CleaningPlanShift(id,name,state)`; `shift` asumido como `{id,name,state}` del plan B3); `OperationsRepository.todayPlans` (`view=today`), `ManageOperations.todayPlans`, `OperationsViewModel.refreshToday()` y estado `todayPlans/todayLoading/todayLoaded/todayFailure` (recorre hasta 8 páginas; se recarga solo tras registrar una limpieza si ya estaba cargado). ADMIN/CHEF sin cambios.
**Proxy web.** `training-proxy.mjs` proyecta `keyPoints` (lector, catálogo, borrador; en entrada solo se reenvía si viene, ausente/null conserva), campos de avance, `percent` de asignaciones, `streak`, `xpEarned`, `nextCourse` y acepta `completedLessonId` en PUT progress. `operations-proxy.mjs` admite `view=today` solo en `cleaning-plans` (una vez, valor `today`) y proyecta `today`. Pruebas node ampliadas.
**Verificación.** `compileKotlinJvm`, `jvmTest` completo (incluye `*Training*`/`*Operations*` nuevas: `TrainingPendientesTest`, `OperationsTodayTest`), `compileKotlinWasmJs` y `node --test web/*.test.mjs` (156/156) OK. Nota: con Node 25 `node --test web/` (directorio) falla con MODULE_NOT_FOUND; usar el glob `web/*.test.mjs`.

## Estado F2

Hecho (UI del frontend), sin commit ni push. Rama `feat/pendientes-personal` del frontend. `compileKotlinJvm`, `jvmTest` completo, `compileKotlinWasmJs` y `node --test web/*.test.mjs` (156/156) OK.

**Archivos (frontend, `composeApp/src/commonMain/...`).**
- Design system: `core/designsystem/BpmStreakBadge.kt` (nuevo: `BpmStreakBadge`, `streakTitle`, `streakHint`, `streakWeekMarks`), `BpmIcon.kt` (+`Flame`) y `composeResources/drawable/ic_flame.xml` (Lucide, trazo 2).
- Hoy: `app/WorkerTodayContent.kt` (racha en el panel de apoyo, tarjeta «Tareas de hoy», barra de avance en el siguiente paso), `app/App.kt` (pasa `dailyOperations` y `locations`; callback `onOpenCourse` al curso), `app/TrainingContent.kt`.
- Curso: `training/presentation/CourseSteps.kt` (`lessonStepStates` usa `completedLessonIds` si el servidor informa `totalLessons > 0`, si no la aproximación; `courseRing`), `CourseOverview.kt`, `LessonFlow.kt` (`outlineStates`, `KeyPointsCard` «Lo esencial»), `LearnHub.kt` (`assignmentProgress`), `TrainingLearnerScreens.kt` (anillo en «Continuar», barra en tarjetas).
- Autoría: `training/domain/{TrainingRules,TrainingDraftEdits}.kt` (`DraftIssueKind.KEY_POINTS`, `keyPointsIssue`, `add/set/removeKeyPoint`, `normalized` recorta y descarta vacíos), `presentation/TrainingAdminScreen.kt` (`KeyPointsEditor`: hasta 5 campos de una línea, contador de 200, «+ Agregar idea»/«Quitar»).
- Resultado: `EvaluationFlow.kt` («+N XP» con `xpEarnedChip`, aviso con `xpRepeatedNotice`, botón «Siguiente curso: {título}» y «Ruta: {ruta}»), `TrainingScreen.kt` (parámetro `onOpenCourse`).
- Tareas: `operations/presentation/{WorkerTasks,WorkerTaskItem,OperationsScreen}.kt` (`todayChip`, `todayDoneBy`, `todayDetail`, `sortTodayPlans`, `homeTodayPlans`, `TodayPlanRow`, `WorkerTodayList`).
- Pruebas: `WorkerTasksTest` (reescrita), `CourseStepsTest`, `TrainingDraftRulesTest`, `ResultXpTest` y `BpmStreakBadgeTest` (nuevas).

**Decisiones/desviaciones.**
- `dueTone` y `minutesOfDay` se eliminaron junto con su prueba (ya nada depende de la hora del cliente); `dueChipText`, `sortPlansByDueTime`/`sortByDueTime` se conservan.
- «Por hacer» del WORKER usa solo `todayPlans` (todas las páginas, sin búsqueda ni paginación; `refreshToday` al abrir la pestaña); la lista paginada de planes sigue para ADMIN/CHEF. Al abrir la pestaña `resource(PLANS)` aún dispara la consulta paginada que ya existía (no se usa en pantalla).
- Hoy abre el `OperationsViewModel` compartido con la sede (única o primera asignada) y llama `refreshToday`; eso también dispara la carga de «Hechas». Con varias sedes muestra «Sede: X» y «Ver todas». Con tareas visibles se oculta la fila «Registrar limpieza» de «Tu turno» (queda «Incidencias»).
- Chip: Hecho > Vencida > «hasta HH:mm» > nombre del turno (Neutral); «quién» va en la línea de apoyo («Hecho por ti»/«Hecho por tu equipo»).
- «Sin XP nuevo: ya tenías este puntaje» solo con FINAL aprobada y `xpEarned == 0` (un FINAL reprobado con 0 XP no lo muestra). «Siguiente curso» solo tras FINAL aprobada.
- Avance real = `progress.totalLessons > 0`; con servidor anterior (o caché sin esos campos) se conserva la aproximación. Curso COMPLETED: todas hechas siempre.
- Racha: puntos de los últimos 7 días derivados de `currentDays`/`activeToday` (sin pedir fechas al servidor); «Tu mejor racha» si `longestDays > currentDays`.
- Sin pruebas de UI Compose (el repo no las tiene); revisión visual pendiente en dispositivo/web.
