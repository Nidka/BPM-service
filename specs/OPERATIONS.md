# Operación diaria

`features/operations` contiene recepción de insumos, consulta PEPS y limpieza.
El dominio no depende de HTTP, PostgreSQL ni otro adaptador. Los casos de uso
reciben puertos de catálogos, recepciones y limpieza; PostgreSQL implementa una
unidad de trabajo y HTTP proyecta DTO públicos. La composición vive en
`internal/core/app`. No hay identidades, sedes ni catálogos ficticios en producción.

## Permisos

Cada petición verifica identidad confirmada, usuario activo, empresa y membresía
vigentes. Las operaciones de sede verifican también su estado y asignación actual.
El rol y responsable salen de esa autorización, nunca del JSON recibido.

| Rol | Catálogos y planes | Recepciones y PEPS | Limpieza realizada |
|---|---|---|---|
| SUPER_ADMIN | Crear, consultar y configurar criterio térmico | Supervisar sedes autorizadas; sin membresía no registra | Supervisar |
| ADMIN | Crear, consultar y configurar criterio térmico | Registrar recepción, consumo y descarte, y consultar todas las sedes activas de su empresa (opera como CHEF) | Ejecutar y consultar todas las sedes activas de su empresa; solo es responsable de sus propias ejecuciones |
| CHEF | Consultar | Registrar recepción y consultar sedes asignadas | Ejecutar y consultar sedes asignadas |
| WORKER | Consultar planes de sedes asignadas; sin catálogos de insumos | Sin acceso | Ejecutar y consultar solo sus propias ejecuciones |

No se deriva un rol de la cuenta Google, del cliente ni del nombre de una sede.
Los bloqueos de lectura sobre membresías y sedes impiden que una revocación se
confirme durante una escritura autorizada con un contexto que ya cambió.

## Contrato HTTP

Prefijo `/api/v1/organizations/{organizationID}`. El identificador de organización
conserva el contrato existente `[A-Za-z0-9_-]{1,64}`; los identificadores de negocio
son UUID. OpenAPI en `internal/core/httpserver/docs/openapi.yaml` es el contrato
detallado de propiedades y límites.

| Ruta | Métodos |
|---|---|
| `/suppliers` | GET, POST |
| `/products` | GET, POST |
| `/products/{productID}/temperature-policy` | PUT |
| `/supply-receipts` | GET, POST |
| `/supply-receipts/{receiptID}` | GET |
| `/storage-lots` | GET |
| `/cleaning-plans` | GET, POST |
| `/cleaning-executions` | GET, POST |
| `/cleaning-executions/{executionID}` | GET |

Las listas aceptan `pageSize`, `pageToken` y `query`. Recepciones, lotes, planes y
limpiezas realizadas requieren además `locationId`; no se consulta una sede por
omisión. Los cursores quedan vinculados a usuario, empresa, sede, recurso y búsqueda.
La respuesta es `{data: [...], pagination: {nextPageToken: ...}}`. Un detalle o una
escritura usa `{data: ...}`. Cuerpos estrictos: no se aceptan campos desconocidos,
roles ni responsables enviados por el cliente. Límite JSON: 512 KiB en UTF-8,
tanto en la API como en el proxy web; las respuestas del proxy se acotan a 4 MiB.

## Recepción y PEPS

Una recepción admite de 1 a 50 productos. La cantidad es una cadena decimal exacta,
positiva, hasta `999999999.999` y tres decimales; nunca pasa por coma flotante.
`ACCEPTED`, `OBSERVED` y `REJECTED` son decisiones distintas. Solo un producto
aceptado crea un lote, dentro de la misma transacción que la recepción y auditoría.
Observados y rechazados necesitan motivo y no admiten ubicación ni etiqueta de stock.

Toda recepción nueva informa envase íntegro o dañado. Para aceptar se exige envase
íntegro, almacenamiento y vencimiento no anterior al **día de recepción en la zona
horaria de la sede**. El reloj o zona del dispositivo no define ese día. Cuando el
producto requiere temperatura, la medición es obligatoria; aceptarlo exige un rango
configurado y que la medición esté dentro de él. No se inventan rangos sanitarios.
Un producto anterior que requiere temperatura pero carece de criterio permite
observación o rechazo con medición y motivo, hasta que el administrador lo configure.

La recepción conserva el criterio térmico usado, ubicación y etiqueta como instantánea.
Cambiar el producto después no reinterpreta una recepción histórica. El criterio se
actualiza con `rowVersion`; un cambio concurrente produce conflicto.

PEPS ordena por fecha/hora de recepción, identificador del ítem y del lote. La fecha
de vencimiento se informa por separado y no cambia ese orden. La lectura de legado
admite lote con UUID distinto al ítem, código de lote ausente y envase no informado;
no rellena datos desconocidos como si hubieran sido observados.

## Limpieza y confirmación única

Los planes pertenecen a una sede y admiten frecuencia diaria, semanal o mensual,
instrucciones, producto y hora de referencia. La ejecución exige un plan activo en
esa misma sede. Una observación o tarea no completada requiere explicación. Cada
ejecución registra un hecho; la frecuencia no impone artificialmente un único evento
por día ni introduce turnos que todavía no están implementados.

Las escrituras exigen `Idempotency-Key` de 8 a 100 caracteres visibles y un UUID
estable. La transacción bloquea la operación de empresa, compara el hash normalizado,
guarda resultado y auditoría una sola vez. Un mismo UUID con otros datos se rechaza.
Una respuesta perdida se puede recuperar con el mismo UUID y contenido incluso con
otra clave, después de reautorizar al actor. Para ese reintento se devuelve el hecho
original aunque cambie después el criterio térmico o se inactive el plan. No se crea
un registro nuevo para «comprobar» el anterior. El hash interno no se expone por HTTP.

## Migración V5 y activación

`V5__daily_operations.sql` es aditiva; no modifica V1, V2 ni V4. Agrega criterio térmico,
versión optimista, instantáneas de recepción, hash y orden de ítems, índices y unicidad
por ítem de recepción. Las columnas nuevas de hechos anteriores son anulables para
conservar lo desconocido. No transforma envases antiguos en íntegros ni inventa límites.

Antes de aplicar el índice único, el operador debe comprobar:

```sql
SELECT organization_id, receipt_item_id, COUNT(*)
FROM storage_lots
GROUP BY organization_id, receipt_item_id
HAVING COUNT(*) > 1;
```

Si devuelve filas, investigar su origen antes de migrar. No eliminar ni fusionar lotes
automáticamente. Con respaldo y migraciones previas verificadas, aplicar V5 una sola
vez con `ON_ERROR_STOP=1`, antes de iniciar esta API. `RequireSchema` impide arrancar
con esquema incompleto. No reaplicar V1 a una base existente, no borrar volúmenes y
no incluir V3 local de capacitación en esta entrega. El pipeline de pruebas sí instala
V1, V2, V4 y V5 sobre una base vacía y aislada. La activación de producción sigue el
procedimiento del operador de Railway; esta implementación no ejecuta esa migración.

## Alcance y verificación

Esta entrega no incluye consumo/descarte de stock, programación por turno, fotos de
limpieza ni cola offline de nuevas recepciones. Los formularios recuperables de KMP
protegen el contenido y reintentan la misma operación; no confirman hechos sin red.
Incidencias, notificaciones y los módulos posteriores conservan su orden en FEATURES.md.

Las pruebas de PostgreSQL real cubren autorización por empresa/sede, usuario inactivo,
revocación concurrente, cantidades, criterio térmico, legado anterior a V5, orden PEPS,
50 ítems, rollback e idempotencia concurrente sin duplicar recepción, lote ni auditoría.
Se complementan con dominio, aplicación, HTTP, `go vet` y ejecución con detector de
carreras. El proxy valida y proyecta el mismo contrato sin reenviar cookies o roles.

## Turnos por sede, movimientos de stock y limpieza por turno (V13)

Esta sección amplía las anteriores; no cambia contratos existentes. Requiere
`V13__operations_shifts_stock.sql` (el arranque falla con `operations shifts and stock
migration V13 is required` si falta). Convenciones comunes de este módulo: prefijo
`/api/v1/organizations/{organizationID}`, UUID en minúsculas, JSON camelCase, fechas ISO-8601
UTC, respuestas `{ "data": ... }` (listas con `pagination.nextPageToken`), errores
`{ "errors": [{ "code", "message", "fields?" }], "requestId" }`, cuerpos estrictos (campo
desconocido = 400), límite JSON 512 KiB. Las escrituras exigen `Idempotency-Key` (8 a 100
caracteres visibles) y devuelven **200** también al recuperar un reintento. Las listas aceptan
`pageSize` (1-100, 25 por omisión), `pageToken` y `query`; `locationId` es obligatorio.

### Turnos

Un turno es un nombre más hora de inicio y fin **locales de la sede** (zona IANA de la sede, no la
del dispositivo). `startTime`/`endTime` son `HH:MM` de 24 horas. Si `endTime < startTime` el turno cruza
la medianoche (p. ej. `22:00`-`06:00`). El inicio es inclusivo y el fin excluyente. Dos turnos activos de
una sede no pueden solaparse ni repetir nombre (sin distinguir mayúsculas). Sugerencia de catálogo:
MAÑANA 06:00-14:00, TARDE 14:00-22:00, NOCHE 22:00-06:00; son datos que define el ADMIN, no constantes.

Objeto `Shift`:

```json
{"id":"uuid","locationId":"uuid","name":"Mañana","startTime":"06:00","endTime":"14:00",
 "timezone":"America/Lima","crossesMidnight":false,"active":true,"openNow":true,
 "rowVersion":1,"createdAt":"2026-11-10T15:00:00Z"}
```

`openNow` lo calcula el servidor con su reloj y la zona de la sede en cada lectura.

| Método y ruta | Roles | Cuerpo | Respuesta |
|---|---|---|---|
| `GET /shifts?locationId=&status=ACTIVE\|ALL&query=` | ADMIN, SUPER_ADMIN, CHEF y WORKER con acceso a la sede (`ALL` solo ADMIN/SUPER_ADMIN, otro rol 403) | - | lista de `Shift` ordenada por `startTime` |
| `POST /shifts` | ADMIN, SUPER_ADMIN | `{id, locationId, name (2-60), startTime, endTime}` | `Shift` |
| `GET /shifts/{shiftID}` | como la lista | - | `Shift` |
| `PUT /shifts/{shiftID}` | ADMIN, SUPER_ADMIN | `{name, startTime, endTime, rowVersion}` | `Shift` con `rowVersion+1` |
| `POST /shifts/{shiftID}/deactivate` | ADMIN, SUPER_ADMIN | `{rowVersion}` | `Shift` con `active:false` (repetible) |

Errores: 403 `ACCESS_DENIED` (rol o sede), 404 `NOT_FOUND` (turno/sede de otra empresa o inexistente),
422 `VALIDATION_ERROR` (`name`, `startTime`, `endTime`, `rowVersion`; solape en `startTime`; nombre repetido
en `name`), 409 `RESOURCE_CONFLICT` (mismo `id` con otros datos, `rowVersion` obsoleto o turno inactivo),
409 `SHIFT_IN_USE` (hay checklists programados activos con ese turno), 400 `INVALID_PAGINATION`.
Cambiar el horario afecta cálculos futuros; las revisiones y limpiezas ya registradas conservan su `shiftId`.
Un turno desactivado no se reactiva: se crea otro (su nombre puede reutilizarse).

### Limpieza por turno

`POST /cleaning-plans` acepta ahora `frequency: "PER_SHIFT"` además de DAILY/WEEKLY/MONTHLY.
`POST /cleaning-executions` acepta `shiftId` (UUID, opcional). Reglas: el turno debe ser activo y de la misma
sede (si no, 404); la hora `executedAt` convertida a la zona de la sede debe caer en su horario, si no 422
`SHIFT_OUT_OF_HOURS` con `fields.shiftId`; un plan `PER_SHIFT` exige `shiftId` (422 `fields.shiftId`).
La respuesta de la ejecución añade `shiftId` y `shiftName` cuando existen (se omiten si no hay turno).

### Movimientos de stock (consumo y descarte)

Operan sobre `storage_lots` y se escriben en la misma transacción que la auditoría. El saldo
(`quantityAvailable` del lote) se descuenta con aritmética decimal exacta; nunca queda negativo. Un lote
cuyo saldo llega a 0 pasa a `CONSUMED` (si el último movimiento fue consumo) o `DISCARDED` (descarte) y no admite
más movimientos. Los movimientos son inmutables (ni se editan ni se borran; una corrección es una recepción
o un nuevo registro). Roles: registrar = **CHEF** de la sede asignada o **ADMIN** con membresía activa (todas las sedes de su empresa; SUPER_ADMIN sin membresía no registra); consultar historial = ADMIN, SUPER_ADMIN
y CHEF de la sede; WORKER sin acceso.

`POST /stock-movements` (200):

```json
{"id":"uuid","locationId":"uuid","lotId":"uuid","type":"CONSUMPTION|DISCARD","quantity":"0.5",
 "reason":"EXPIRED|DETERIORATED|CONTAMINATION|OTHER","note":"texto","clientOperationId":"uuid",
 "clientCreatedAt":"2026-11-10T15:00:00Z"}
```

- `quantity`: cadena decimal exacta > 0, hasta 9 enteros y 3 decimales. No se aceptan cero, negativos, notación científica ni más decimales.
- `type=CONSUMPTION`: sin `reason`. Un lote vencido (fecha de vencimiento anterior al día local de la sede) no se consume: 409 `LOT_EXPIRED`; se registra descarte con `EXPIRED`.
- `type=DISCARD`: `reason` obligatorio; con `OTHER` la `note` (máx. 500) es obligatoria.
- `clientCreatedAt` no puede estar más de 5 minutos en el futuro. `id` y `clientOperationId` son UUID estables del cliente.
- Idempotencia: la misma clave o el mismo `id` con idéntico cuerpo devuelve el movimiento original sin volver a descontar (también con otra `Idempotency-Key`). El mismo `id` con otro cuerpo, o hecho por otro usuario, es 409 `RESOURCE_CONFLICT`.

Respuesta (`StockMovement`): los campos anteriores más `productName`, `unit`, `batchCode`, `balanceAfter` (saldo del
lote tras el movimiento, decimal canónico), `performedBy`, `performedByName`, `createdAt`.

Errores: 403 `ACCESS_DENIED`; 404 `NOT_FOUND` (lote inexistente, de otra sede o empresa); 409
`INSUFFICIENT_STOCK` (saldo menor que la cantidad o lote ya agotado); 409 `LOT_EXPIRED`; 422 `VALIDATION_ERROR`
(`quantity`, `type`, `reason`, `note`, `clientCreatedAt`, ids).

`GET /stock-movements?locationId=&lotId=&type=CONSUMPTION|DISCARD&query=&pageSize=&pageToken=`: historial por sede, opcionalmente por
lote y tipo, del más reciente al más antiguo; `query` busca en producto, código de lote y nota. Un cursor solo vale
con los mismos `locationId`, `lotId`, `type` y `query`. `GET /stock-movements/{movementID}` devuelve un movimiento.

`GET /storage-lots` añade `receivedQuantity` (cantidad aceptada en la recepción); `quantityAvailable` es el saldo
actual tras los movimientos. El orden PEPS no cambia; los lotes agotados siguen listándose con `status` `CONSUMED`/`DISCARDED`.

### Verificación de esta sección

Pruebas con PostgreSQL real: tenant y sede ajenos, roles, saldo negativo, cero/negativos/decimales, idempotencia (misma
clave, otra clave, mismo UUID con otro cuerpo), consumo de lote vencido, concurrencia (cuatro descuentos simultáneos de
0.5 sobre 1.125 dejan exactamente dos éxitos y saldo 0.125), inmutabilidad, auditoría, solape y turnos que cruzan la
medianoche, zona horaria de Lima con inicio inclusivo y fin excluyente.

## SUPER_ADMIN que también es miembro
Un SUPER_ADMIN con membresía activa en la empresa opera con el rol de su membresía (ADMIN, CHEF o WORKER), incluidas las limitaciones de sede (un CHEF solo opera y ve sus sedes asignadas). Sin membresía sigue como supervisor y recibe 403 al operar. Ver `ORGANIZATIONS.md`.
