# Migraciones PostgreSQL

## Ubicación y formato

Todas las migraciones viven en `migraciones/`, dentro del backend:

```text
migraciones/
  V1__baseline.sql
  V2__descripcion_del_cambio.sql
  V3__otra_descripcion.sql
```

Se usa el formato `V<numero>__<descripcion>.sql`, con número creciente y sin
reutilizar versiones. `V1__baseline.sql` representa la estructura inicial ya
instalada.

## Reglas

1. Una migración desplegada es inmutable.
2. Todo cambio posterior se agrega en un archivo nuevo y avanza hacia adelante.
3. Cada archivo debe ser transaccional cuando PostgreSQL lo permita.
4. Los cambios de datos deben ser idempotentes o estar protegidos por la versión.
5. Índices grandes y cambios incompatibles requieren estrategia de despliegue.
6. Nunca se incluyen credenciales, datos reales ni URLs de conexión.
7. El pipeline debe ejecutar primero migraciones y después iniciar la API.

## Runner previsto

El runner se implementará como un comando separado, `cmd/migrate`, no dentro
del arranque HTTP. Usará `DATABASE_URL`, un bloqueo asesor de PostgreSQL y una
tabla de control `schema_migrations`. Así se evita que varias réplicas intenten
migrar simultáneamente y se mantiene una sola responsabilidad por proceso.

Para una base existente que ya contiene el baseline, `V1` se registra como
aplicada sin volver a ejecutar sus `CREATE TABLE`. En instalaciones nuevas se
ejecuta desde cero.

Hasta implementar el runner, la ejecución es manual y debe usar
`ON_ERROR_STOP=1`; Railway no debe ejecutar automáticamente el baseline sobre la
base ya creada.

`V2__authentication_sessions.sql` añade exclusivamente el estado técnico de
sesiones. Debe aplicarse antes de desplegar el backend que la exige; consultar
[SESSIONS.md](SESSIONS.md). Los volúmenes Docker ya existentes no ejecutan scripts
de inicialización nuevos automáticamente: aplicar V2 una vez en la base local
correspondiente. No borrar volúmenes con datos para forzar la inicialización.

## Evidencias de controles (V4)

`V4__checklist_evidence_items.sql` sustituye el vínculo de evidencia a respuesta
por pregunta, preservando las asociaciones existentes. Aplicar una sola vez
después de V2, antes del backend que la exige; consultar [MEDIA.md](MEDIA.md).
V3 local de capacitación permanece fuera de esta entrega. No ejecutar el
baseline de nuevo ni incluir borradores de migraciones en el despliegue.

## Operación diaria (V5)

`V5__daily_operations.sql` agrega criterio térmico, instantáneas de recepción,
hashes e índices de operación diaria. Aplicar después de V4 y antes del backend
que exige este esquema. Comprobar antes los lotes duplicados por ítem siguiendo
[OPERATIONS.md](OPERATIONS.md); no se eliminan datos antiguos automáticamente.
V3 local de capacitación sigue fuera de esta entrega.

## Incidencias (V6)

`V6__incidents.sql` añade catálogo versionado e historial inmutable, versión
optimista y recibos privados de comandos. Aplicar después de V5, antes de la API
que exige este esquema. Revisar el preflight de [INCIDENTS.md](INCIDENTS.md); no
borrar ni fusionar acciones o asociaciones históricas automáticamente. V3 local
de capacitación continúa fuera del despliegue.

## Alertas y notificaciones (V7)

`V7__alerts_notifications.sql` añade versiones, snapshots, deduplicación, leases
y destinos privados. Aplicar después de V6 y antes de la API/worker de esta
entrega. El preflight rechaza entregas legadas duplicadas por organización,
aviso y canal; revisar su historial manualmente, sin borrar ni fusionar registros.
La migración no crea reglas, cuentas, contenidos ni asignaciones de prueba.
V3 local sigue fuera de esta entrega. Ver [ALERTS.md](ALERTS.md) y
[NOTIFICATIONS.md](NOTIFICATIONS.md); el worker se despliega aparte.

## Academia BPM (V8)

`V8__training_academy.sql` añade `row_version` del borrador, `request_hash` de
intentos, unicidad de una sola versión DRAFT y una sola PUBLISHED por curso,
constancia única por (organización, usuario, versión), índices y restricciones de
contenido (`NOT VALID`). Aplicar después de V7 (o de V2 si V4–V7 ya están) y antes
de la API que exige este esquema; el arranque falla de forma explícita si falta.
El preflight se detiene si existen borradores/publicadas o constancias duplicadas;
revisarlas manualmente, sin borrar ni fusionar. No crea cursos ni datos de prueba.
V3 local de capacitación sigue reservada y no se usa. Ver [TRAINING.md](TRAINING.md).

## Gestión de cuentas (V12)

`V12__accounts_management.sql` añade `row_version` a organizaciones, sedes y membresías,
la tabla `organization_invitations` (solo hash del token) y amplía `business_type`
(`RELATED_SERVICE` pasa a `OTHER_FOOD_SERVICE`). Aplicar después de V8 (V7 es necesaria
para el outbox) y antes de la API/worker que la exigen. El preflight se detiene ante tipos
de establecimiento desconocidos. V3 sigue reservada. Ver [ORGANIZATIONS.md](ORGANIZATIONS.md).

## Investigación CAP (V9)

`V9__research_cap.sql` añade bandera CAP, versión de fila del borrador, respuesta correcta,
puntajes e idempotencia de entregas, unicidad de participante y de código seudónimo,
documentos de consentimiento, concesiones de acceso de investigación, necesidades
detectadas y triggers de inmutabilidad. Aplicar después de V8 y antes de la API; el
arranque falla de forma explícita si falta. El preflight se detiene ante participantes,
códigos o versiones DRAFT/PUBLISHED duplicados; revisarlos sin borrar ni fusionar. No
crea datos. Ver [RESEARCH.md](RESEARCH.md).

## Reportes y uso (V10)

`V10__reports_usage.sql` añade la función `bpm_report_timezone`, `report_settings`,
`compliance_certificates` (inmutable por trigger), `weekly_report_deliveries`, la columna e índice
único `usage_events.client_event_id` (idempotencia de eventos), restricciones `NOT VALID` de uso e
índices de lectura. Aplicar después de V8 (y V9 si existe) y antes de la API/worker que lo exige;
ambos fallan al arrancar si falta. No crea datos de prueba ni toca filas existentes. V3 sigue
reservada. Ver [REPORTS.md](REPORTS.md).

## Operaciones ampliadas (V13)

`V13__operations_shifts_stock.sql` agrega `location_shifts`, `shift_id` en programaciones, revisiones y limpiezas, `stock_movements` (append-only) y una sola versión vigente por plantilla (`change_note`, `request_hash`). Aplicar después de V5 (y V4) con `ON_ERROR_STOP=1`, antes del backend que la exige. Preflight incluido: aborta si una plantilla tiene más de una versión PUBLISHED. Ver [OPERATIONS.md](OPERATIONS.md) y [CHECKLISTS.md](CHECKLISTS.md).

## Avisos para el ADMIN que opera (V14)

`V14__alerts_admin_operates.sql` reemplaza `alert_recipient_authorized` (de V7) para que el aviso `CHECKLIST_MISSING` también sea visible al ADMIN; no agrega tablas ni columnas. Aplicar después de V7 con `ON_ERROR_STOP=1`. La autovalidación de incidencias (`selfValidated`) se calcula y se audita; no requiere migración.

## Progreso por lección y "Lo esencial" (V15)

`V15__training_lesson_progress.sql` es aditiva: crea `training_lesson_progress` (lecciones completadas por persona, origen `VIEWED` o `MODULE_PASSED`), agrega `training_lessons.key_points` (JSONB, arreglo de hasta 5 puntos clave) y `training_attempts.xp_awarded` (NULL en intentos anteriores) y el índice `cleaning_executions_plan_idx (organization_id, cleaning_plan_id, executed_at DESC)`. Aplicar después de V11 (y V1) con `ON_ERROR_STOP=1` y **antes** de desplegar la API: la Academia falla al arrancar si falta (`training migration V15 is required`). No borra ni reescribe datos; el contenido ya publicado queda con `keyPoints` vacío hasta una nueva publicación. Ver [TRAINING.md](TRAINING.md).

## Subtítulos de lecciones (V16)

`V16__training_lesson_captions.sql` es aditiva: agrega `training_lessons.captions_vtt` (texto WebVTT, vacío por
defecto, hasta 100 000 caracteres) para RF-45. Aplicar después de V15 con `ON_ERROR_STOP=1` y **antes** de
desplegar la API: la Academia falla al arrancar si falta (`training migration V16 is required`). No borra ni
reescribe datos; las lecciones existentes quedan sin subtítulos hasta que el administrador los cargue.

## Buscador de normativa (V17)

`V17__normative_assistant.sql` es aditiva: crea `normative_documents` (normativa en texto que carga el
SUPER_ADMIN, `ACTIVE`/`ARCHIVED`) y `normative_fragments` (párrafos con `search_vector` generado en español sin
tildes e índice GIN). Aplicar antes de desplegar la API: el servicio falla al arrancar si falta (`normative
migration V17 is required`). No usa servicios externos. La base debe tener `LC_CTYPE` UTF-8 (por ejemplo
`en_US.UTF-8`); con `C`, `lower()` no convierte vocales con tilde y la paginación por nombre de operación diaria
pierde registros.

## Webhooks salientes (V18)

`V18__webhooks.sql` es aditiva: crea `webhook_endpoints` (URL HTTPS, secreto de firma, tipos de aviso, último
resultado). Aplicar antes de desplegar la API y el worker: ambos fallan al arrancar si falta (`webhooks migration
V18 is required`), y el evaluador de alertas consulta la tabla al crear cada alerta. Ver [WEBHOOKS.md](WEBHOOKS.md).

## Aplicar desde macOS/Linux

`./scripts/aplicar-migraciones.sh --revisar` muestra qué falta sin cambiar nada; sin `--revisar` pide escribir
`SI`, hace un backup completo con `pg_dump` en `backups/` (ignorado por git) y aplica las pendientes en orden con
`ON_ERROR_STOP`. Usa `DATABASE_URL` del entorno o de `.env`. `pg_dump` debe ser de la misma versión principal del
servidor o superior. Ensayado sobre una copia con V1–V15: aplica V16–V18 y la API y el worker arrancan.


## Banco oficial de preguntas (V19)

`V19__training_question_bank.sql` es aditiva: agrega a `training_questions` los metadatos del ítem (`item_code`,
`bank_version`, `bloom_level`, `source_refs`), a `training_attempt_answers` el ítem, su versión y el orden de alternativas
mostrado (`item_code`, `bank_version`, `option_order`) y crea `training_evaluation_draws` (sorteo de cada evaluación) y
`training_practice_results` (práctica y repaso espaciado). No reescribe ni borra datos; la app anterior sigue funcionando
con ella aplicada. Aplicar antes de desplegar la API: la Academia falla al arrancar si falta (`training migration V19 is
required`). El contenido (módulos, unidades y los 100 ítems) se instala por organización con `cmd/catalogo-bpm`
(ver TRAINING.md, «Banco oficial de preguntas»); la migración no carga contenido.

## Videos de la plataforma (V20)

`V20__training_platform_videos.sql` es aditiva: crea `platform_training_videos` (videos del catálogo base, sin organización,
con clave `organizations/plataforma/training/videos/...`) y agrega `training_lessons.platform_video_id` (opcional, excluyente
con el video privado y el de YouTube). No reescribe datos. Aplicar antes de desplegar la API: la Academia falla al arrancar
si falta (`training migration V20 is required`). Los objetos de R2 se copian aparte y se verifican con
`cmd/catalogo-bpm -videos` (ver TRAINING.md, «Catálogo base»).

## Aviso de incidencia asignada (V21)

`V21__alerts_incident_assigned.sql` es aditiva y no cambia tablas (`alerts.alert_type` y `alerts.source_type` son VARCHAR
sin CHECK): reemplaza `alert_recipient_authorized` (conservando V14) y `alert_source_pending` (de V7) para reconocer el tipo
`INCIDENT_ASSIGNED` (fuente `INCIDENT`) y crea la función marcadora `alert_incident_assigned_supported()`. Aplicar después de V7
con `ON_ERROR_STOP=1` (o con `scripts/aplicar-migraciones.sh`). **Desplegar el backend antes de esta migración es seguro**: el
emisor de `incidents` consulta la marca; sin ella omite el aviso con una advertencia y la asignación se confirma igual. Ver
[NOTIFICATIONS.md](NOTIFICATIONS.md) e [INCIDENTS.md](INCIDENTS.md).

## Introducción vista y contacto de soporte (V22)

`V22__onboarding_support_contact.sql` es aditiva: agrega `application_users.onboarding_seen_at` (nula = aún no vio la
introducción de primeros pasos) y crea `platform_settings` (una sola fila con `support_email`, por defecto
`proyectobpmupn@gmail.com`, y `support_whatsapp` opcional). No reescribe datos. Aplicar antes de desplegar la API: falla
al arrancar si falta (`platform settings migration V22 is required`). Rutas: `PUT`/`DELETE /api/v1/me/onboarding`
(marcar o volver a mostrar la introducción; `GET /me` devuelve `onboardingSeenAt`) y `GET`/`PUT /api/v1/platform/support`
(lectura para toda persona activa; edición solo `SUPER_ADMIN`).

## Aviso de problema reportado (V23)

`V23__alerts_incident_reported.sql` es aditiva y no cambia tablas: reemplaza `alert_recipient_authorized` y
`alert_source_pending` (conservando V14 y V21) para reconocer `INCIDENT_REPORTED` (fuente `INCIDENT`) y crea la función
marcadora `alert_incident_reported_supported()`. Al reportar una incidencia se avisa (bandeja + push) a cada ADMIN y a cada
CHEF con la sede, sin incluir a quien reportó; el aviso deja de estar pendiente cuando la incidencia sale de `OPEN`.
**Desplegar el backend antes de esta migración es seguro**: sin la marca no emite el aviso y el reporte sigue igual.
