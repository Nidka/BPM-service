# Organizaciones y espacio de trabajo

## Implementado

`features/organizations` es el segundo recorrido vertical: HTTP → aplicación →
puerto UnitOfWork/Store → PostgreSQL. La composición manual está en `module.go`
y `core/app/container.go`. No importa adaptadores de identidad: recibe el UID
del middleware y vuelve a validar usuario y permisos en PostgreSQL.

| Ruta bajo `/api/v1` | Acceso |
|---|---|
| GET /organizations | Organizaciones activas con membresía activa; SUPER_ADMIN ve todas |
| POST /organizations | Solo SUPER_ADMIN |
| GET /organizations/{id}/workspace | Miembro activo o SUPER_ADMIN |
| GET /organizations/{id}/locations | Sedes activas autorizadas, paginadas |
| GET /organizations/{id}/members | ADMIN del tenant/SUPER_ADMIN, paginado |
| GET /organizations/{id}/courses | Cursos publicados y progreso propio, paginado |
| GET /organizations/{id}/checklist-summaries | Resúmenes de plantillas activas, paginados |
| GET /organizations/{id}/incidents | Incidencias de sedes autorizadas, paginadas |
| POST /organizations/{id}/locations | ADMIN del tenant o SUPER_ADMIN |
| POST /organizations/{id}/members | ADMIN del tenant o SUPER_ADMIN |

Todas requieren token Firebase válido, correo verificado y cuenta BPM activa.
El tenant de la URL es una solicitud de acceso, no una autorización. Un ADMIN de
una organización no administra otra. CHEF/WORKER solo ven sus sedes asignadas;
la lista de personas solo se devuelve a administradores. Los trabajadores ven
únicamente incidencias que reportaron en sus sedes autorizadas.

El workspace incluye catálogo de cursos publicados con el progreso del usuario,
plantillas activas e incidencias visibles. Son consultas de base, no ejecución de
los módulos: no hay lecciones, evaluaciones, respuestas ni cambios de estado.
Las descripciones se resumen a 500 caracteres. Workspace devuelve la primera
página, `nextPageTokens` independientes y `totals` de sedes permitidas, cursos
publicados y plantillas publicadas. Los indicadores cuentan el conjunto autorizado
completo y no dependen de cuántos registros cargó el cliente.

## Altas, auditoría y errores

- Organización: razón social, nombre comercial, tipo, identificación fiscal y
  contacto opcional. Un identificador fiscal vacío se guarda NULL, no como un
  valor único compartido por todas las empresas.
- Sede: nombre, dirección, distrito/teléfono opcionales; zona America/Lima.
- Miembro: cuenta BPM activa existente por correo, rol ADMIN/CHEF/WORKER y
  sedes del mismo tenant. CHEF/WORKER requieren al menos una. ADMIN accede a todas.
  No es una invitación: no crea cuentas Firebase ni envía correo.
- Las operaciones y `audit_events` se confirman en una transacción. created_by
  y updated_by salen del actor autenticado; las fechas las genera PostgreSQL.
- UUID cliente estable para organización/sede: reintento idéntico devuelve 200
  sin duplicar ni reescribir datos/auditoría. Mismo ID con otro contenido: 409.
  Membresía existente: 409, nunca cambia su rol silenciosamente.
- JSON hasta 16 KiB, campos desconocidos rechazados. errors[] usa code/message/
  fields; no se exponen SQL, tokens, cadenas de conexión ni excepciones.

## Puesta en marcha y límites

Se usa V1 existente sin modificarla ni reaplicarla. No hace falta una migración
nueva para estas altas. Nunca ejecutar la baseline contra la base con datos.

El primer SUPER_ADMIN debe designarlo el responsable de la plataforma mediante
un procedimiento administrativo autorizado y auditado. Ni el registro público
ni este cambio convierten automáticamente cuentas USER en administradores.
Luego: crear organización → crear sede → agregar ADMIN existente → incorporar
colaboradores a sus sedes. No hay endpoint público de elevación de plataforma.

Pendientes: edición/desactivación/transferencia de administración, invitaciones,
dispositivos, sincronización offline, ejecución de controles,
multimedia R2, lecciones/evaluaciones, operación diaria, alertas e investigación.
Los UUID de alta facilitan reintentos; no equivalen a una cola offline.

## Verificación

Unitarias: validación sin acceso a DB. HTTP: token, correo no verificado,
campos desconocidos, errores por campo y sanitización. Integración PostgreSQL:
baseline real en esquema aislado de `bpm_test`, múltiples tenants, roles,
usuario/membresía inactivos, sedes ajenas, repetición, auditoría y catálogo.
`TEST_DATABASE_URL` es solo de pruebas; no usar las credenciales de Railway.

Contrato completo: `internal/core/httpserver/docs/openapi.yaml`.

## Paginación de lectura

`GET /organizations` y las listas de la tabla aceptan exclusivamente `pageSize`
(1–100), `pageToken` (hasta 2048 caracteres URL-safe) y `query` (hasta 160
caracteres, sin controles). Repetir parámetros, añadir parámetros desconocidos
o cambiar el contexto del cursor devuelve 400 `INVALID_PAGINATION`. El cliente
reinicia con cursor vacío. No se interpreta `query` como SQL: `%` y `_` se buscan
literalmente, mediante subcadena sin distinción de mayúsculas.

```json
{"data": [], "pagination": {"nextPageToken": ""}}
```

El cursor vacío es el fin de la colección. Cada consulta obtiene `pageSize + 1`
filas y no expone la fila adicional. Se ordena por nombre/título y después ID;
miembros usa nombre/UID e incidencias fecha de creación descendente/ID
ascendente, conservando el orden previo. Las comparaciones
SQL y ORDER BY usan la misma tupla y colación. No se usa OFFSET ni se descargan
todos los registros para cortarlos en memoria. Se puede cambiar el tamaño de
página manteniendo el cursor, pero no usuario, organización, recurso ni búsqueda.

El formato binario interno del cursor está versionado y se codifica para URL;
el cliente debe tratarlo como cadena opaca. Su huella detecta cambios de contexto,
no es una firma ni una credencial. No contiene tokens de sesión ni secretos.
Cada solicitud vuelve a consultar usuario, membresía, rol y sedes. Alterar una
posición jamás sustituye esos filtros. La clave de anclaje viaja completa: borrar
la fila de la página anterior no impide continuar. No se conserva una instantánea
entre solicitudes: altas, bajas o cambios de nombre pueden modificar el recorrido;
actualizar vuelve a la primera página. Los totales corresponden al momento de la
consulta, no a una reserva inmutable del conjunto.

Workspace acepta únicamente `pageSize`; los siguientes cursores se consumen en
las rutas de cada lista. Las incidencias consultan directamente las sedes activas
permitidas; ya no se filtran por una página previa de sedes. WORKER sigue viendo
solo sus reportes. GET de miembros devuelve 403 a CHEF/WORKER, nunca una lista
vacía que parezca autorizada.

Compatibilidad: las rutas existentes sin query conservan hasta 100 resultados y
la forma de `data`; se añaden metadatos. Al usar parámetros el tamaño por defecto
es 25; los nuevos endpoints usan 25 incluso sin query. Publicar primero backend
y después frontend. No cambia configuración, esquema, migraciones ni escrituras;
esta implementación no se despliega ni modifica la base de producción por sí sola.
Los índices del esquema existente se conservan; antes de crecer fuera del piloto
se deben medir planes reales para decidir índices adicionales de orden/búsqueda.

La primitiva técnica `shared/application/pagination` tiene dos consumidores reales,
organizaciones y checklists. Las consultas y permisos siguen dentro de cada feature.
El handler interpreta HTTP, aplicación autoriza y coordina, y PostgreSQL filtra y
limita. No se añadió un repositorio genérico.

Verificación: 137 organizaciones, sedes, miembros y cursos; nombres repetidos,
búsqueda fuera de los primeros 100, límites y parámetros inválidos, anclaje borrado,
roles, tenant ajeno, revocación entre páginas e incidencias en la sede 137.
Las pruebas locales usan PostgreSQL 14 temporal por socket privado y esquemas
`bpm_test`; CI mantiene PostgreSQL 18. Nunca usan Railway ni cuentas reales.

Referencias: [orden y límites PostgreSQL](https://www.postgresql.org/docs/18/queries-limit.html)
y [AIP-158](https://google.aip.dev/158) para cursores, autorización por solicitud y
compatibilidad. El contrato BPM define su propia validación estricta 1–100, en
lugar de adoptar la coerción de tamaños recomendada por AIP.

## Comprobación previa al alta

POST `/api/v1/organizations/{organizationID}/members/check` recibe únicamente
`email` en JSON. Requiere sesión verificada y ADMIN del tenant o SUPER_ADMIN.
Devuelve `READY`, `ALREADY_MEMBER` o `UNAVAILABLE`, sin nombres, UID ni datos de
otras organizaciones. No hay búsqueda pública ni alta automática: las cuentas
inexistentes e inactivas comparten respuesta. No modifica membresías ni auditoría.
Un perfil BPM se registra a través de identidad después de verificar el correo;
no se consulta el directorio Firebase desde el navegador.

El frontend comprueba la cuenta antes de mostrar rol, sedes y confirmación.
Cambiar correo, usuario o empresa invalida la comprobación. Restaurar un borrador
exige comprobar de nuevo. La respuesta no es una autorización reutilizable:
POST members vuelve a comprobar permisos, usuario activo, pertenencia y sedes.
Una carrera con otra alta conserva el conflicto existente. Sin migraciones nuevas.

## Sugerencias dirigidas para incorporar personas

POST `organizations/{id}/members/search` recibe solo `query` en JSON y autoriza
ADMIN de esa organización o SUPER_ADMIN antes de consultar cuentas. Es una
capacidad explícita de incorporación: busca prefijos de correos de perfiles BPM
activos de la plataforma, no solo la página actual del equipo. No revela nombres,
UID, estado privado, roles ni membresías de otras empresas. CHEF, WORKER y
administradores de otro tenant reciben denegación, incluso con una búsqueda exacta.

Mínimo 3 caracteres, máximo 320 bytes, sin espacios/controles. Prefijo literal y
sin distinción de mayúsculas; `%` y `_` no son comodines. Se devuelven como máximo
5 correos ordenados, sin cursor ni catálogo completo; se afina escribiendo más.
Se excluyen todas las membresías existentes del tenant, también inactivas, porque
el alta no las reactiva. La consulta hereda el límite HTTP de solicitudes y la
política de sesión; no crea invitaciones, cuentas, auditorías ni membresías.

`data.suggestions` es una lista de correos. `exactStatus` es null con un prefijo;
con un correo completo devuelve READY, ALREADY_MEMBER o UNAVAILABLE. Cero
sugerencias parciales no significa correo sin registrar. Inexistencia/inactividad
comparten UNAVAILABLE. El cliente no persiste sugerencias ni autorizaciones y
vuelve a consultar al recuperar un borrador. Una respuesta tardía no puede
restaurar otra consulta, cuenta o empresa. POST members mantiene su revalidación.

El endpoint exacto `/members/check` permanece para clientes instalados anteriores;
el nuevo formulario no lo utiliza ni muestra un paso manual para comprobar.
No requiere migraciones ni variables nuevas. Pruebas con PostgreSQL aislado cubren
límites, orden, normalización, exclusión, caracteres literales, cuentas inactivas,
sin escrituras y denegaciones de rol/tenant. Publicar backend antes del cliente.

## Validación de los formularios

Identidad y organizaciones reutilizan `shared/domain/input`. Persona de contacto
es un nombre personal opcional de hasta 160 caracteres y no admite números.
Razón social, nombre comercial, sede y dirección sí admiten números; sus límites
se conservan (200/200/160/300). Identificación fiscal admite texto hasta 20;
distrito hasta 120. Teléfono opcional: 7–15 dígitos y hasta 30 caracteres de
formato, con + inicial, espacios, guiones y paréntesis. El correo opcional debe
estar completo, con dominio con punto y hasta 320 bytes. Controles y saltos de
línea se rechazan. Error 422 con la clave del campo; no se consulta DB con
formatos inválidos. No hay migración ni modificación de registros ya existentes.

## Gestión de cuentas, invitaciones, dispositivos y tipo de establecimiento

Requiere la migración `V12__accounts_management.sql` (el arranque de API y worker falla
si falta). Todas las rutas van bajo `/api/v1`, exigen token Firebase válido con correo
verificado y cuenta BPM activa. JSON de hasta 16 KiB, campos desconocidos rechazados
(400 `INVALID_REQUEST`), camelCase y fechas ISO-8601 UTC. Errores con el formato de
`API_ERRORS.md`. «Gestor» = ADMIN de esa organización o SUPER_ADMIN. El tenant sale
siempre de la membresía autenticada; una organización ajena o inactiva responde
403 `ACCESS_DENIED`, y un miembro/sede/invitación/dispositivo inexistente o de otro
tenant responde 404.

### Control de concurrencia

Organización, sede, membresía e invitación tienen `rowVersion` (entero >= 1, se
incrementa en cada cambio). Todo PATCH/acción sobre una fila exige el `rowVersion`
leído; si no coincide: 409 `VERSION_CONFLICT` (el cliente vuelve a leer y reintenta).
Los cambios de cuentas de una organización se serializan con un bloqueo de fila de la
organización, por lo que dos gestores no pueden dejarla sin ADMIN activo.
Un PATCH sin cambios efectivos devuelve el recurso sin incrementar versión ni auditar.

### Tipo de establecimiento (RF-49)

`businessType`: `RESTAURANT`, `CAFETERIA` (cafetín), `CATERING`, `OTHER_FOOD_SERVICE`.
Obligatorio al crear (422 con `fields.businessType`) y editable por el gestor. V12
migra el valor anterior `RELATED_SERVICE` a `OTHER_FOOD_SERVICE` (valor por defecto
documentado); como entrada, `RELATED_SERVICE` se acepta solo como alias y se guarda
como `OTHER_FOOD_SERVICE`. Se devuelve en `organization` de
`GET /organizations/{id}/workspace`, en `GET /organizations` y en las respuestas de edición.

### Formas de salida

- Organización: `{id, legalName, tradeName, taxId, businessType, contactName, contactPhone,
  contactEmail, rowVersion}` (`rowVersion` presente en lecturas, ausente en el alta).
- Sede: `{id, name, address, district, contactPhone, timezone, status, rowVersion}`
  (`status` `ACTIVE|INACTIVE`; en el alta `status`/`rowVersion` se omiten).
- Miembro: `{id, name, email, role, locationIds[], status, rowVersion}` (se añaden `status`
  y `rowVersion` al objeto ya existente; `status` `ACTIVE|INACTIVE`).

### Listados con filtro de estado

`GET /organizations/{org}/members?status=ACTIVE|INACTIVE` (gestor; predeterminado ACTIVE) y
`GET /organizations/{org}/locations?status=ACTIVE|INACTIVE` (INACTIVE solo gestor; si no,
403). Además de `status` solo se aceptan `pageSize`, `pageToken`, `query` (400
`INVALID_PAGINATION` en otro caso, incluido un status inválido o repetido). Respuesta:
`{"data":[...],"pagination":{"nextPageToken":""}}`. El cursor queda ligado al estado.

### Edición de organización y sede

| Método y ruta | Roles | Cuerpo |
|---|---|---|
| `PATCH /organizations/{org}` | gestor | `{legalName(2-200), tradeName(<=200), taxId(<=20), businessType, contactName(<=160), contactPhone, contactEmail, rowVersion}` |
| `PATCH /organizations/{org}/locations/{locationId}` | gestor | `{name(2-160), address(3-300), district(<=120), contactPhone, timezone, status, rowVersion}` |

Son reemplazos completos de los campos editables (se reenvían todos). Validaciones iguales
a las del alta; `timezone` es una zona IANA válida (ej. `America/Lima`), `status`
`ACTIVE|INACTIVE`. Respuesta 200 `{"data": <organización|sede>}` con la nueva `rowVersion`.
Errores: 403, 404 (sede ajena), 409 `VERSION_CONFLICT`, 409 `RESOURCE_CONFLICT`
(nombre de sede o `taxId` duplicados), 422. Se audita `organization.updated` /
`location.updated` (metadata: campos modificados). Desactivar una sede no borra ni
reasigna a nadie: deja de listarse como activa y no puede asignarse a miembros.

### Miembros (RF-04)

| Método y ruta | Roles | Cuerpo |
|---|---|---|
| `PATCH /organizations/{org}/members/{userId}` | gestor | `{role: ADMIN/CHEF/WORKER, locationIds: [uuid], rowVersion}` |
| `POST .../members/{userId}/deactivate` | gestor | `{rowVersion}` |
| `POST .../members/{userId}/reactivate` | gestor | `{rowVersion}` |
| `POST .../members/{userId}/transfer-admin` | gestor | `{rowVersion, actorRole?, actorLocationIds?}` |

- PATCH: `locationIds` sin duplicados, <= 100, de sedes activas del tenant; CHEF/WORKER
  requieren al menos una (422 `fields.locationIds`). Funciona también sobre miembros
  inactivos. Respuesta `{"data": <miembro>}`.
- deactivate: baja lógica (nunca borrado físico). Retira los dispositivos del miembro en
  esa organización (token push eliminado) y revoca sus sesiones técnicas
  (`authentication_sessions`, SESSIONS.md) cuando ya no tiene otra membresía activa ni rol
  SUPER_ADMIN; las sesiones no están ligadas a un tenant, y el acceso al tenant se
  deniega de todos modos en cada petición porque la membresía se revalida. Idempotente:
  si ya está inactivo devuelve el miembro actual.
- reactivate: restablece rol y sedes previos. CHEF/WORKER sin sedes activas asignadas:
  422 `fields.locationIds` (editar con PATCH primero). Cuenta BPM inactiva: 404
  `MEMBER_NOT_AVAILABLE`. Idempotente si ya está activo.
- transfer-admin: `{userId}` pasa a ADMIN y quien transfiere (ADMIN miembro) pasa a
  `actorRole` (`CHEF|WORKER`, obligatorio) con `actorLocationIds` (>= 1). Un SUPER_ADMIN no
  miembro omite ambos campos. `rowVersion` es el del destino, que debe estar activo
  (409 `MEMBER_INACTIVE`) y no puede ser quien transfiere (422 `fields.userId`).
  Respuesta `{"data":{"newAdmin":<miembro>,"formerAdmin":<miembro>|null}}`.
- Regla del último ADMIN: no se puede degradar ni desactivar al único ADMIN activo (con
  cuenta activa) -> 409 `LAST_ADMIN_REQUIRED`, tampoco por un SUPER_ADMIN.
- Un miembro desactivado recibe 403 `ACCESS_DENIED` en todas las rutas del tenant.
- Auditoría: `membership.updated|deactivated|reactivated|admin_transferred_in|admin_transferred_out`.

### Invitaciones por correo

| Método y ruta | Roles |
|---|---|
| `POST /organizations/{org}/invitations` | gestor |
| `GET /organizations/{org}/invitations?status=&query=&pageSize=&pageToken=` | gestor |
| `POST /organizations/{org}/invitations/{id}/resend` | gestor |
| `POST /organizations/{org}/invitations/{id}/revoke` | gestor |
| `POST /invitations/accept` | cualquier usuario BPM activo con correo verificado |

Crear: `{id (UUID cliente), email, role, locationIds}` (mismas reglas que PATCH de miembro).
201 `{"data": <invitación>}`; el mismo `id` con el mismo contenido repite 200 sin duplicar
ni reenviar el correo; mismo `id` con otro contenido: 409 `RESOURCE_CONFLICT`.
Invitación: `{id, email (minúsculas), role, locationIds[], status, expiresAt, lastSentAt,
createdAt, sendCount, rowVersion}`; `status` = `PENDING|ACCEPTED|REVOKED|EXPIRED`
(`EXPIRED` es el estado efectivo de una pendiente vencida). Nunca se devuelve el token.
Vigencia 7 días. Errores: 409 `MEMBER_ALREADY_EXISTS` (ya hay membresía, incluso inactiva;
usar reactivar), 409 `INVITATION_ALREADY_PENDING`, 403, 422.

Listar: `status` opcional (vacío = todas), orden por correo e id, paginado por cursor.

Reenviar: genera un token nuevo (el anterior deja de servir), renueva 7 días y encola otro
correo; funciona con pendientes y vencidas. 409 `INVITATION_NOT_PENDING` si está aceptada o
revocada; 429 `INVITATION_RESEND_TOO_SOON` si pasó menos de 1 minuto desde el último envío.
Sin cuerpo. Revocar: pendiente/vencida -> REVOKED (idempotente); aceptada ->
409 `INVITATION_NOT_PENDING`. Ambas devuelven `{"data": <invitación>}`.

Aceptar: cuerpo `{"token": "<43 caracteres base64url>"}`. El correo del token Firebase
verificado y el del perfil BPM deben coincidir (sin distinguir mayúsculas) con el de la
invitación; si no, 403 `INVITATION_EMAIL_MISMATCH`. Crea la membresía (o reactiva una
inactiva con el rol/sedes invitados) y marca la invitación ACCEPTED en una transacción.
200 `{"data":{"organizationId","role","locationIds"}}` (solo sedes aún activas).
Errores: 404 `INVITATION_NOT_FOUND` (token inexistente o mal formado, o sustituido por un
reenvío), 409 `INVITATION_EXPIRED`, `INVITATION_ALREADY_USED`, `INVITATION_REVOKED`,
`MEMBER_ALREADY_EXISTS`, `INVITATION_NOT_PENDING` (todas las sedes se desactivaron).
El cliente abre el enlace `INVITATION_ACCEPT_URL?token=...`, autentica y llama a este endpoint.

Seguridad: token de 256 bits aleatorios; solo se guarda su SHA-256. El token sin cifrar viaja
únicamente en el evento de outbox `organizations.invitation.requested.v1` (misma transacción
que la invitación) y se borra del payload al enviarse, reenviarse, revocarse o aceptarse.
El worker (`bpm-worker`) envía el correo por Resend (`RESEND_API_KEY`, `RESEND_FROM_EMAIL`) con
clave de idempotencia `bpm-invitation/<dedupKey>`, descarta eventos sustituidos y difiere
mientras no haya proveedor. Variable opcional `INVITATION_ACCEPT_URL` (página del cliente
que recibe `?token=`); sin ella el correo incluye el token tal cual. Los eventos de outbox
duran 23 h: si el correo no salió en ese plazo, reenviar la invitación. No hay límite por IP
específico en `accept`; el token no es adivinable.

### Dispositivos propios

| Método y ruta | Acceso |
|---|---|
| `GET /me/devices?pageSize=&pageToken=` | el propio usuario |
| `DELETE /me/devices/{deviceId}` | el propio usuario |

Dispositivo: `{id, organizationId, organizationName, platform (ANDROID|IOS|WEB), status,
lastSeenAt (ISO|null), createdAt}`; nunca el token push ni la instalación. El listado solo
incluye los ACTIVE del usuario autenticado en todas sus organizaciones; un ADMIN no ve ni
puede revocar los de otros (404). DELETE sin cuerpo ni parámetros (400 si los hay)
desactiva el dispositivo, borra su token push y es idempotente; responde
`{"data": <dispositivo con status INACTIVE>}`. El registro sigue en
`POST /organizations/{org}/notifications/devices` (NOTIFICATIONS.md).

### Códigos de error nuevos

`VERSION_CONFLICT`(409), `LAST_ADMIN_REQUIRED`(409), `MEMBER_ALREADY_EXISTS`(409),
`MEMBER_INACTIVE`(409), `RESOURCE_NOT_FOUND`(404), `INVITATION_NOT_FOUND`(404),
`INVITATION_ALREADY_PENDING`(409), `INVITATION_EXPIRED`(409), `INVITATION_ALREADY_USED`(409),
`INVITATION_REVOKED`(409), `INVITATION_NOT_PENDING`(409), `INVITATION_EMAIL_MISMATCH`(403),
`INVITATION_RESEND_TOO_SOON`(429).

### Verificación

Pruebas PostgreSQL aisladas (cadena V1-V8 + V12): último ADMIN (incluida la carrera de
dos degradaciones simultáneas), tenant ajeno, rol insuficiente, concurrencia por
`rowVersion`, desactivado sin acceso con sesiones y dispositivos retirados, invitación
vencida/reutilizada/revocada/de otro correo/sustituida, token solo como hash y borrado del
outbox, tipo de establecimiento y dispositivos ajenos.
