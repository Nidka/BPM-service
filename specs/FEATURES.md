# Especificación breve de features

Estas especificaciones indican qué debe implementar cada módulo. Los endpoints
son una guía inicial y pueden ajustarse antes de publicarse en OpenAPI.

## Orden y dependencias

| Orden | Módulo | Depende de |
|---:|---|---|
| 1 | Identidad y tenancy | Firebase, PostgreSQL |
| 2 | Checklists y sincronización | Identidad |
| 3 | Multimedia | Identidad, checklists, R2 |
| 4 | Operación diaria | Identidad, multimedia |
| 5 | Incidencias | Identidad, multimedia |
| 6 | Alertas y notificaciones | Identidad, outbox |
| 7 | Academia BPM | Identidad, multimedia |
| 8 | Investigación CAP | Identidad |
| 9 | Auditoría, uso y reportes | Todos los anteriores |

## 1. Identidad y tenancy

Estado: identidad y alta de perfil implementadas. `features/organizations`
incorpora selección de tenant, alta de organizaciones/sedes y adición de cuentas
existentes al equipo. Permisos, límites y contrato en `ORGANIZATIONS.md`.
Dispositivos, invitaciones, edición y desactivación siguen pendientes.

- Tablas: `application_users`, `organizations`, `locations`,
  `organization_memberships`, `membership_locations`, `mobile_devices`.
- Casos de uso: resolver identidad Firebase, consultar sesión, seleccionar
  organización activa, listar sedes autorizadas, administrar miembros y
  registrar dispositivos FCM.
- Endpoints actuales: `GET /me`, `GET|POST /organizations`,
  `GET /organizations/{id}/workspace`, `POST /organizations/{id}/locations`
  y `POST /organizations/{id}/members` (prefijo `/api/v1`).
- Permisos: `SUPER_ADMIN` opera plataforma; `ADMIN`, `CHEF` y `WORKER` siempre
  dentro de una membresía activa.
- Regla crítica: el `organization_id` efectivo sale de la membresía verificada.
- Pruebas: token inválido/revocado, usuario inactivo, rol incorrecto, sede ajena
  y manipulación de tenant.

## 2. Checklists y sincronización offline

Estado: primera versión vertical implementada (plantilla publicada, catálogo,
borrador, respuestas y envío) y documentada en `CHECKLISTS.md`. Programación
por sede/frecuencia y revisión única del período implementadas. Turnos, versiones
posteriores y evidencias siguen pendientes. El historial paginado de
ejecuciones y la separación de roles se describen en CHECKLISTS.md.
El catálogo publicado ya tiene paginación y detalle autorizado independiente.

- Tablas: `checklist_templates`, `checklist_template_versions`,
  `checklist_items`, `checklist_assignments`, `checklist_runs`,
  `checklist_answers`, `idempotency_records`.
- Casos de uso: crear plantilla, publicar versión inmutable, asignar a sede,
  crear borrador, guardar respuestas y finalizar ejecución.
- Endpoints: catálogo/versiones/asignaciones y
  `POST|PATCH /checklist-runs`, `POST /checklist-runs/{id}/submit`.
- Offline: UUID generado por cliente, `Idempotency-Key`, `client_operation_id`,
  `device_id`, tiempos cliente/servidor y `row_version`.
- Reglas: el servidor calcula cumplimiento; `N/A` no entra al denominador;
  ejecuciones finalizadas no se sobrescriben.
- Pruebas: reintentos idénticos, hash diferente con la misma clave, conflicto de
  versión, envío duplicado y cinco operaciones acumuladas sin conexión.

## 3. Multimedia privada

- Tablas: `media_files`, `checklist_media`, `cleaning_media`, `incident_media`.
- Casos de uso: solicitar carga, confirmar objeto, solicitar descarga y marcar
  carga fallida/eliminada.
- Endpoints: `POST /media/uploads`, `POST /media/{id}/confirm`,
  `POST /media/{id}/download`.
- Flujo: validar tenant/sede/MIME/tamaño/hash, crear `PENDING_UPLOAD`, firmar PUT,
  comprobar HEAD, bytes acotados, formato y SHA-256 real antes de marcar `AVAILABLE`.
- Entrega por pregunta y reglas completas: [MEDIA.md](MEDIA.md).
- Regla crítica: clave generada en servidor bajo
  `organizations/{org}/locations/{location}/media/...`; nunca URL pública.
- Pruebas: objeto ajeno, MIME/tamaño incorrecto, checksum diferente, URL vencida
  y carga huérfana.

## 4. Operación diaria

Estado: recorrido estructurado implementado para catálogos, criterio térmico,
recepciones, lectura PEPS y planes/ejecuciones de limpieza. Contrato, permisos,
legado y activación V5 en [OPERATIONS.md](OPERATIONS.md). Fotos de limpieza,
consumo/descarte, turnos y cola offline de nuevas operaciones siguen pendientes.

- Tablas: `suppliers`, `products`, `supply_receipts`, `supply_receipt_items`,
  `storage_lots`, `cleaning_plans`, `cleaning_executions`, `cleaning_media`.
- Subdominios internos: recepción de insumos, almacenamiento/PEPS y limpieza.
- Casos de uso: mantener catálogos, recibir lote, decidir aceptación, consultar
  vencimientos, programar limpieza y registrar ejecución.
- Permisos: `ADMIN` configura y supervisa; `CHEF` recibe insumos, consulta PEPS y
  realiza limpieza; `WORKER` ejecuta limpieza en sedes asignadas y solo consulta
  sus propias ejecuciones.
- Offline: recepciones y ejecuciones usan UUID, idempotencia y evidencias
  posteriores al dato estructurado.
- Pruebas: temperatura/decisión, lote duplicado, vencimiento, sede ajena y
  evidencia asociada a otra organización.

## 5. Incidencias y acciones correctivas

Estado: catálogo versionado, reporte por sede, asignación, atención, corrección,
validación independiente e historial implementados. Fotos privadas antes/después
reutilizan media; contrato, permisos, legado y activación V6 en
[INCIDENTS.md](INCIDENTS.md). Constancia interna y notificaciones corresponden a
los módulos posteriores; no se emiten certificados en esta entrega.

- Tablas: `incident_catalog`, `incidents`, `corrective_actions`,
  `incident_media`.
- Casos de uso: registrar incidencia, asignar responsable, iniciar/resolver,
  validar cierre y consultar historial.
- Estados: `OPEN → ASSIGNED → IN_PROGRESS → RESOLVED → VERIFIED/CLOSED`; solo se
  permiten transiciones expresamente definidas en dominio.
- Permisos: trabajador reporta; chef/admin asigna y valida según política.
- Regla crítica: no se borra historial; correcciones y evidencias son eventos
  trazables.
- Pruebas: transición inválida, cierre sin evidencia cuando es obligatoria,
  responsable fuera de sede y separación ejecutor/validador.

## 6. Alertas, notificaciones y outbox

- Tablas: `alert_rules`, `alerts`, `notification_deliveries`, `outbox_events`,
  `mobile_devices`.
- Casos de uso: evaluar reglas, crear alerta, marcar vista/atendida y entregar por
  FCM o correo.
- Procesamiento: el caso de uso escribe negocio + outbox en una transacción; un
  worker toma eventos con bloqueo, reintentos y backoff.
- Canales: Firebase FCM y Resend. Un fallo externo nunca revierte la operación de
  negocio ya confirmada.
- Idempotencia: `event_type + aggregate_id` y estado de entrega impiden duplicar
  mensajes visibles.
- Pruebas: reintentos, proveedor caído, worker concurrente, token FCM inválido y
  evento agotado.

Estado: reglas configurables, bandeja privada y entrega transaccional implementadas.
Contrato en [ALERTS.md](ALERTS.md), proveedores/activación en
[NOTIFICATIONS.md](NOTIFICATIONS.md). Requiere V7 y servicio worker separado;
la compilación no implica envíos ni despliegue real.

## 7. Academia BPM

- Tablas: `training_courses`, `training_course_versions`, `training_lessons`,
  `training_questions`, `training_assignments`, `training_attempts`,
  `training_attempt_answers`, `training_progress`, `training_certificates`,
  `training_achievements`, `user_achievements`.
- Casos de uso: publicar curso versionado, asignar, guardar avance, responder
  evaluación, finalizar intento y emitir constancia.
- Regla crítica: el servidor calcula puntaje, aprobación, XP e insignias.
- Versionado: una publicación no modifica intentos ni constancias anteriores.
- Offline: catálogo descargable y progreso local sincronizado de forma
  idempotente.
- Pruebas: intento repetido, respuestas manipuladas, versión antigua,
  calificación límite y constancia sin aprobación.

## 8. Investigación CAP

- Tablas: `research_participants`, `consent_records`, `survey_instruments`,
  `survey_instrument_versions`, `survey_questions`, `survey_submissions`,
  `survey_answers`.
- Casos de uso: registrar participante seudónimo, consentimiento, publicar
  instrumento, enviar pretest/postest y exportar datos anonimizados.
- Regla crítica: una entrega por participante, versión y momento; instrumentos
  publicados son inmutables.
- Privacidad: exportaciones no incluyen email, UID Firebase ni identificadores
  directos.
- Permisos: acceso de investigación separado de la operación cotidiana y
  auditado.
- Pruebas: consentimiento ausente, duplicidad PRETEST/POSTTEST, versión incorrecta
  y comprobación de anonimización.

## 9. Auditoría, uso y reportes

- Tablas/vistas: `audit_events`, `usage_sessions`, `usage_events`,
  `daily_checklist_compliance`, `organization_training_summary`.
- Auditoría: registrar actor, tenant, acción, entidad, fecha, request ID y
  metadatos mínimos; tratar eventos como append-only.
- Uso: registrar únicamente eventos necesarios para la investigación, evitando
  cuerpos, respuestas sanitarias y datos sensibles en metadatos.
- Reportes: consultas paginadas y exportaciones autorizadas por tenant y período.
- Observabilidad: correlacionar logs/Sentry con `requestId`, nunca con secretos.
- Pruebas: aislamiento de tenant, paginación, zona horaria de sede, exportación y
  prohibición de actualización/borrado desde repositorios.

## Definición de terminado para cada módulo

1. Dominio y casos de uso sin dependencias de infraestructura.
2. Puertos pequeños y adaptadores PostgreSQL/HTTP separados.
3. Autenticación, autorización de tenant y auditoría probadas con casos negativos.
4. Idempotencia/offline aplicada cuando corresponda.
5. Pruebas unitarias, repositorio con PostgreSQL real y contrato HTTP.
6. OpenAPI, variables y decisiones actualizadas.
7. `gofmt`, `go vet`, tests y race detector aprobados.

