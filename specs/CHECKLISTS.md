# Checklists: primer recorrido operativo

## Alcance implementado

`internal/features/checklists` contiene dominio, casos de uso, PostgreSQL y HTTP.
No añade tablas ni altera `V1__baseline.sql`. Se conecta en el contenedor de
aplicación, usando el pool y el verificador Firebase ya existentes.

1. `ADMIN` del tenant o `SUPER_ADMIN` crea una plantilla con UUID de cliente,
   preguntas y versión 1; se publica en la misma transacción. Una versión
   publicada no se edita.
2. Un miembro activo descarga páginas de plantillas publicadas completas del tenant.
   Los cursores permiten continuar sin limitar el catálogo a las primeras 100.
3. El CHEF con acceso a una sede activa inicia una ejecución con ID,
   `clientOperationId` y `clientCreatedAt` generados por el cliente. Repetir
   exactamente el alta devuelve la ejecución existente sin duplicar auditoría.
4. El ejecutor guarda el conjunto completo de respuestas usando
   `rowVersion` e `Idempotency-Key`. Un reintento idéntico devuelve la respuesta
   original; la misma clave con otros datos o una versión obsoleta da 409.
5. El ejecutor envía el borrador. El servidor valida las preguntas requeridas,
   calcula el cumplimiento y congela la ejecución. `NOT_APPLICABLE` y `TEXT`
   no entran al denominador. Debe haber al menos una pregunta evaluable.

Todas las escrituras relevantes generan `audit_events` en la misma transacción.
Las respuestas HTTP usan `{ "data": ... }`; los errores usan `errors[]` con
`code`, `message` y `fields` cuando corresponda, más `requestId`.

## Rutas

Prefijo `/api/v1/organizations/{organizationID}`. Todas requieren Firebase ID
token con correo verificado y usuario BPM activo.

| Método | Ruta | Permiso |
|---|---|---|
| GET | `/checklists` | miembro activo |
| GET | `/checklists/{templateID}` | miembro activo; última versión publicada completa |
| POST | `/checklists` | ADMIN o SUPER_ADMIN |
| GET | `/checklist-runs` | CHEF en sedes asignadas; ADMIN (todas las sedes) y SUPER_ADMIN supervisan |
| POST | `/checklist-runs` | CHEF asignado a sede |
| GET | `/checklist-runs/{runID}` | CHEF/ADMIN/SUPER_ADMIN con acceso a sede |
| PUT | `/checklist-runs/{runID}/answers` | ejecutor; `Idempotency-Key` |
| POST | `/checklist-runs/{runID}/submit` | ejecutor; `Idempotency-Key` |

El contrato de cuerpos y respuestas vive en `internal/core/httpserver/docs/openapi.yaml`.
El cliente debe mantener una cola local con el mismo ID, clave y cuerpo entre
reintentos; después de 409 `VERSION_CONFLICT`, debe recuperar el run y resolver
el conflicto antes de enviar. Las claves de idempotencia quedan registradas
durante al menos 24 horas; no deben reciclarse.

## Límites conscientes de esta primera base

- Aún no hay edición de plantilla ni publicación de versión 2. Las asignaciones
  por sede y frecuencia se describen abajo. No hay endpoint de archivo.
- La carga de evidencias privadas en R2 será otro módulo. Hasta entonces la API
  no permite crear ítems con evidencia obligatoria. Si existiera uno en datos
  previos, se rechaza el envío de una respuesta fallida para no omitir el control.
- El guardado de respuestas representa el borrador completo, no un parche. Las
  evidencias que se incorporen después exigirán preservar su vínculo al editar.
- El catálogo está paginado; cada plantilla conserva hasta 100 preguntas según
  el contrato de publicación. La ejecución y sus respuestas no se paginan.

## Pruebas

Las pruebas unitarias cubren roles, validación, versión optimista y cálculo de
puntaje. Las pruebas HTTP cubren sesión, errores de forma y sanitización. La
prueba PostgreSQL utiliza `TEST_DATABASE_URL` solo si apunta a una base llamada
`bpm_test`; instala V1 en un esquema temporal y verifica aislamiento, reintentos,
auditoría e inmutabilidad del envío. Nunca se ejecuta sobre producción.

## Catálogo paginado y detalle

GET `/checklists` usa el mismo contrato `pageSize`, `pageToken`, `query` y
`pagination.nextPageToken` de [ORGANIZATIONS.md](ORGANIZATIONS.md). La búsqueda
incluye nombre, área y descripción. La consulta selecciona primero las plantillas
y su versión publicada más reciente en un CTE con límite de página + 1. Después
incorpora todas las preguntas de esas versiones, sin LIMIT sobre las filas unidas.
No se corta una plantilla ni se omiten sus preguntas al alcanzar 10 000 filas.

GET `/checklists/{templateID}` exige autorización nueva del tenant y devuelve
la última versión publicada con todas sus preguntas; 404 si no existe dentro del
contexto autorizado. No admite query. Permite enlaces directos aunque la plantilla
no esté en la página cargada del catálogo. No inicia ni modifica ejecuciones.

Se preservan las rutas existentes y sus `data`, con hasta 100 plantillas sin query
por compatibilidad. No se cambian tablas ni variables. Las pruebas recorren 137
plantillas con 100 preguntas cada una, comprueban el detalle posterior a la página
inicial, tenant ajeno y membresía revocada entre páginas. PostgreSQL temporal y CI
se describen en ORGANIZATIONS.md.

## Historial y separación de funciones (RF-13 y RF-17)

GET `/checklist-runs` consulta revisiones reales por fecha del servidor e ID
descendentes. Usa pageSize/pageToken/query y filtros `scope=mine|team`,
`status=DRAFT|SUBMITTED`. La consulta busca nombre de plantilla, sede y
responsable y devuelve estado, fechas, respuestas guardadas y cumplimiento.
No descarga las respuestas completas hasta abrir el detalle.

CHEF ejecuta y puede retomar sus borradores; solo consulta las sedes asignadas.
ADMIN con membresía activa también opera: inicia, guarda, responde y envía controles
en todas las sedes activas de su empresa (sin asignación de sede), con las mismas reglas
de turno, versión de plantilla, idempotencia y propiedad que un CHEF; el historial y
los reportes lo atribuyen a su usuario igual que a un CHEF. SUPER_ADMIN sin membresía
publica y supervisa, pero no ejecuta. WORKER no accede al historial
ni al detalle. Se revalidan usuario, membresía y sedes en cada página; el cursor
se liga a actor, organización, búsqueda, scope y estado. Una revocación invalida
el acceso aunque se conserve un cursor anterior.

La consulta del historial no inicia otra revisión ni escribe auditoría de
ejecución. Cada revisión nueva sigue siendo una operación explícita con UUID y
fecha: varias revisiones de la misma plantilla son registros distintos, no
duplicados del reintento idempotente. Las revisiones libres siguen permitidas donde no existe programación activa;
las programadas aplican las reglas del siguiente apartado. La exportación RF-17 continúa pendiente.

Pruebas: 137 revisiones, fechas coincidentes, paginación sin duplicados, filtros,
tenant y sede ajenos, rol WORKER, revocación entre páginas y permisos de escritura
por rol. PostgreSQL temporal exclusivo `bpm_test`; no hay migraciones nuevas.

## Programación por sede y período

ADMIN/SUPER_ADMIN programa una plantilla publicada en una sede activa; CHEF
consulta únicamente sus sedes y ejecuta; WORKER no consulta ni ejecuta estos
controles. Se usan `checklist_assignments` y `checklist_runs.assignment_id` de V1,
sin migración ni cambio de credenciales. No se habilitan turnos ni asignación a
una persona: el esquema actual expresa sede y frecuencia, con ejecución CHEF.

| Método | Ruta | Permiso |
|---|---|---|
| GET | `/checklist-assignments` | ADMIN/SUPER_ADMIN o CHEF en sus sedes |
| GET | `/checklist-assignments/{assignmentID}` | mismo alcance; también inactivas |
| POST | `/checklist-assignments` | ADMIN/SUPER_ADMIN |
| POST | `/checklist-assignments/{assignmentID}/deactivate` | ADMIN/SUPER_ADMIN |

Lista activa paginada con pageSize/pageToken/query por plantilla o sede. Devuelve
período vigente y resumen de revisión mediante consulta por lote, sin N+1. Crear
recibe UUID estable, templateId, locationId, frequency y dueTime opcional HH:MM.
GET/POST detalle no aceptan query; desactivar recibe `{}`. Errores públicos y
contrato completo en OpenAPI.

Se permite una programación activa por plantilla/sede. Crear otra devuelve
409 RESOURCE_CONFLICT. El mismo ID/cuerpo recupera el alta sin otra auditoría;
cambiar el cuerpo con ese ID da conflicto. Desactivar es idempotente y conserva
historial, revisiones y respuestas; un borrador ya iniciado puede terminarse.

Períodos según zona IANA de la sede, con fin excluyente: ONCE desde el alta sin
fin; DAILY desde medianoche local; WEEKLY lunes a lunes; MONTHLY del primer día
al siguiente mes. Se usa calendario local, incluyendo cambios de horario, y el
reloj del servidor después del bloqueo transaccional. La hora opcional es una
referencia visible: no retrasa el inicio ni representa una alerta programada.
Las notificaciones y la programación por turno siguen pendientes.

El alta de revisión incluye assignmentId y exige la misma sede y última versión
publicada. Si existe programación activa y se omite el ID se devuelve 409
ASSIGNMENT_REQUIRED; otro ID de revisión en el período devuelve 409
ASSIGNMENT_ALREADY_STARTED. Todos los chefs de la sede comparten ese período;
solo el ejecutor edita su borrador. El ID de revisión original con cuerpo idéntico
se recupera aunque cambie el período o se detenga la programación. La fecha cliente
no permite elegir un período pasado. Una revisión sin programación sigue siendo
posible únicamente donde no exista asignación activa para esa plantilla/sede.

Crear/desactivar bloquea la plantilla; iniciar bloquea plantilla y programación
en ese orden. Se serializan altas simultáneas y se genera auditoría en la misma
transacción. No hay tarea periódica que cree revisiones ni cierre automático.

Pruebas: límites y roles, período diario/semanal/mensual/ONCE, zona local y DST,
137 asignaciones con cursores/búsqueda, tenant/sede ajenos y revocación, alta
idéntica, conflicto de programación, fecha cliente manipulada, guardado después
de desactivar y dos clientes concurrentes: un solo borrador por período.
PostgreSQL aislado bpm_test, vet y race; sin datos de prueba en producción.

## Fotos por pregunta

El editor permite exigir una foto ante No cumple o medición fuera de límites.
La finalización cuenta solo evidencias AVAILABLE de esa pregunta; guardar
respuestas conserva sus IDs y las asociaciones multimedia. Lectura, carga,
confirmación y retiro se describen en [MEDIA.md](MEDIA.md). `evidenceCounts`
acompaña la lectura de la revisión; no es un campo de escritura.

## Programación por turno y versiones de plantilla (V13)

Esta sección amplía las anteriores; requiere `V13__operations_shifts_stock.sql`. Los turnos se definen con
`/shifts` (ver [OPERATIONS.md](OPERATIONS.md)). Formato general de respuestas y errores igual que el resto del módulo.

### Checklists programados por turno

`POST /checklist-assignments` acepta `shiftId` (UUID opcional). Con turno: `frequency` debe ser `DAILY` y no se envía
`dueTime` (422 con `fields.frequency` / `fields.dueTime`); el turno debe existir, estar activo y ser de `locationId`
(si no, 404). Para una plantilla y sede se permite **una programación activa por turno**, o **una sola de día completo**
(sin turno), pero no ambas mezcladas: 409 `RESOURCE_CONFLICT`. El mismo `id` y cuerpo recupera el alta.

Las respuestas de programación (`GET/POST /checklist-assignments...`) añaden:
`templateVersionNumber` (int), `shiftId`, `shiftName`, `shiftStartTime`, `shiftEndTime` (cadenas, vacías sin turno) y
`shiftState`: `OPEN` (turno abierto ahora), `CLOSED` (ya terminó la ocurrencia de hoy) o `UPCOMING` (aún no empieza); vacío sin
turno. Con turno, `periodStartsAt`/`periodEndsAt` son el inicio y fin UTC de la ocurrencia elegida (la abierta; si no, la
de hoy ya cerrada; si no, la próxima), calculados con la zona de la sede, y `run` es la revisión de **esa** ocurrencia: así
el "pendiente del día" de cada turno se ve por separado (una revisión de la mañana no cubre la tarde).

`POST /checklist-runs` con `assignmentId` de una programación por turno: solo se puede iniciar con el turno abierto según el reloj
del servidor y su hora local; si no, 422 `SHIFT_OUT_OF_HOURS`. Solo una revisión por ocurrencia del turno
(409 `ASSIGNMENT_ALREADY_STARTED`); el reintento del mismo `id`/cuerpo se recupera aunque el turno ya haya cerrado. Si una
plantilla tiene programaciones activas y se omite `assignmentId`: 409 `ASSIGNMENT_REQUIRED`. La revisión (`GET /checklist-runs/{id}`,
altas y guardados) añade `shiftId`, `shiftName` (si hubo turno) y `templateVersionNumber`.

### Versiones de plantilla

Una versión publicada es inmutable. Editar = publicar una versión nueva; la anterior pasa a `ARCHIVED` en la misma transacción y
solo existe una `PUBLISHED` por plantilla. Las revisiones ya iniciadas **conservan su versión** (preguntas, respuestas y número) y
se pueden guardar, enviar y consultar aunque su versión esté archivada. Las programaciones y el catálogo siempre usan la
versión vigente. Nombre, área y descripción de la plantilla no cambian en esta entrega.

| Método y ruta | Roles | Respuesta |
|---|---|---|
| `POST /checklists/{templateID}/versions` | ADMIN, SUPER_ADMIN | 201 `{data: plantilla}` con la versión nueva (`versionId`, `versionNumber`, `items`) |
| `GET /checklists/{templateID}/versions?pageSize=&pageToken=` | miembro activo | lista de resúmenes, de la más nueva a la más antigua |
| `GET /checklists/{templateID}/versions/{versionID}` | miembro activo | plantilla con las preguntas de esa versión (vigente o archivada) |

Cuerpo de publicación (campos desconocidos = 400):

```json
{"versionId":"uuid-nuevo","baseVersionNumber":1,"changeNote":"opcional, máx. 500",
 "items":[{"id":"uuid","sectionName":"Higiene","label":"...","responseType":"COMPLIANCE|TEXT|TEMPERATURE|NUMBER",
           "displayOrder":1,"required":true,"unit":"","minimumValue":null,"maximumValue":null,
           "evidenceRequiredOnFailure":false}]}
```

Las preguntas siguen las mismas reglas de creación (1 a 100, UUID propios y únicos incluso respecto de versiones previas, orden
único, rango para TEMPERATURE/NUMBER). El cliente genera un UUID nuevo para cada pregunta de la nueva versión.
Resumen de versión: `{id, templateId, versionNumber, status:"PUBLISHED|ARCHIVED", changeNote, itemCount, publishedAt}`.

Errores: 403 `ACCESS_DENIED`, 404 `NOT_FOUND` (plantilla/versión ajena), 422 `VALIDATION_ERROR` (`versionId`, `baseVersionNumber`,
`changeNote`, `items`), 409 `VERSION_CONFLICT` (`baseVersionNumber` no es la última: alguien publicó antes; recargar y rehacer),
409 `RESOURCE_CONFLICT` (mismo `versionId` con otro contenido o UUID de pregunta repetido). Repetir `versionId` con el mismo contenido
devuelve la misma versión (idempotente). `POST /checklist-runs` con una versión ya archivada: 409 `TEMPLATE_VERSION_OUTDATED`
(recargar el catálogo).

Pruebas: roles y tenant ajeno, versión base obsoleta, reintento idempotente y mismo UUID con otro contenido, archivado de la
anterior, ejecución antigua que conserva versión y se envía, inicio con versión archivada, asignación que apunta a la vigente,
turno fuera de horario, turno abierto/próximo, unicidad por turno y zona horaria distinta (Lima/Tokio).

## SUPER_ADMIN que también es miembro
Un SUPER_ADMIN con membresía activa en la empresa opera con el rol de su membresía (ADMIN, CHEF o WORKER), incluidas las limitaciones de sede (un CHEF solo opera y ve sus sedes asignadas). Sin membresía sigue como supervisor y recibe 403 al operar. Ver `ORGANIZATIONS.md`.
