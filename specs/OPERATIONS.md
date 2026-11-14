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
| SUPER_ADMIN / ADMIN | Crear, consultar y configurar criterio térmico | Supervisar sedes autorizadas; no registrar recepción como chef | Supervisar |
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
