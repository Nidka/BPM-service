# Incidencias y acciones correctivas

`features/incidents` implementa el catálogo versionado, los reportes por sede,
la asignación de un plan, su atención y la validación independiente. PostgreSQL
es la fuente de verdad; el módulo no importa otras features. El módulo multimedia
conserva la carga, confirmación, descarga y eliminación privada de fotos.

## Permisos y recorrido

- ADMIN y SUPER_ADMIN publican criterios y nuevas versiones; supervisan cualquier
  sede activa autorizada del tenant. CHEF supervisa sus sedes asignadas.
- CHEF y WORKER reportan incidencias. Solo CHEF/WORKER activos con membresía y
  asignación vigentes a la misma sede son responsables elegibles.
- WORKER ve únicamente los reportes propios o incidencias con una acción asignada,
  siempre dentro de sus sedes vigentes. La lista filtra esa visibilidad en SQL.
- ASSIGN exige supervisor, responsable elegible, descripción y plazo futuro.
  Crea una acción PENDING y cambia OPEN a ASSIGNED.
- START y RESOLVE los ejecuta exclusivamente el responsable. START cambia a
  IN_PROGRESS; RESOLVE exige explicación y cambia a RESOLVED.
- VERIFY y RETURN requieren supervisor distinto del responsable. RETURN exige
  motivo y vuelve a IN_PROGRESS. VERIFY cierra la incidencia y verifica la acción.
  El ejecutor nunca valida su propia corrección, aunque sea CHEF o administrador.

Se autoriza usuario, organización, membresía y sede en cada solicitud. Las
escrituras conservan esos registros mediante `FOR SHARE`, serializan recibos del
tenant con un bloqueo asesor y bloquean la incidencia con `FOR UPDATE` antes de
leer sus acciones/evidencia. Las mutaciones de multimedia toman el mismo bloqueo.
`rowVersion` avanza junto con la acción y el evento; un comando con versión
obsoleta devuelve conflicto sin cambiar datos. `allowedActions` es una ayuda
personalizada y no sustituye las comprobaciones del servidor.

Una incidencia nueva solo recibe un plan. Los datos antiguos con varias acciones
siguen visibles; los comandos que necesitan una acción única devuelven conflicto.
No hay borrado de incidencias ni transición libre.

## Criterio y evidencia

Los códigos nuevos son mayúsculos `[A-Z0-9_-]{1,40}`. Cada publicación conserva un
UUID y número de versión inmutables. Crear produce versión 1; publicar desde la
última requiere `expectedVersion` y produce el número siguiente. Las listas solo
muestran la última versión de cada código. Un reporte conserva el UUID publicado:
no cambia su nombre, criterio, riesgo ni exigencia de evidencia por una publicación
posterior. La base rechaza UPDATE/DELETE del catálogo y del historial de eventos.

Si ese criterio exige fotos, VERIFY necesita al menos una foto AVAILABLE BEFORE
sin acción y una AVAILABLE AFTER vinculada a la acción que se verifica, del mismo
tenant e incidencia/sede. Solo cuentan JPG/PNG; una reserva pendiente, archivo
fallido, eliminado, PDF u otra acción no permite cerrar. El conteo sucede bajo
el bloqueo de la incidencia para serializarlo con las mutaciones de multimedia.
Una foto solo cuenta si tiene exactamente una asociación total entre
`incident_media`, `checklist_media` y `cleaning_media`. La evidencia heredada con
varios destinos queda bloqueada y exige revisión por un operador; nunca se
eliminan o fusionan asociaciones automáticamente. No se guardan URLs públicas
ni se duplica el adaptador R2.

## Contrato HTTP

Prefijo `/api/v1/organizations/{organizationID}`; sesión Firebase/BPM y correo
verificado. Los POST requieren `Idempotency-Key` de 8–100 caracteres visibles.
JSON estricto UTF8 hasta 512 KiB; se rechazan campos desconocidos, repetidos,
valores null y campos que no correspondan al comando. `evidenceRequired` es
obligatorio incluso cuando vale false. Todas las escrituras devuelven HTTP 200.

| Método y ruta | Función |
|---|---|
| GET /incident-catalog | Últimas versiones, búsqueda y página |
| POST /incident-catalog | Publicar código nuevo |
| POST /incident-catalog/{catalogID}/versions | Publicar siguiente versión |
| GET /incidents | Lista por `locationId` obligatorio, `status` opcional y búsqueda |
| POST /incidents | Reportar con UUID y `clientOperationId` estables |
| GET /incidents/{incidentID} | Detalle, acciones, permisos y conteos |
| GET /incidents/{incidentID}/assignees | Personas elegibles de la sede, solo supervisores |
| POST /incidents/{incidentID}/actions | ASSIGN, START, RESOLVE, VERIFY o RETURN |
| GET /incidents/{incidentID}/history | Eventos inmutables autorizados |

Listas: `{data:[],pagination:{nextPageToken}}`; escrituras/detalle: `{data:Entity}`.
`pageSize` predetermina 25, acepta 1–100 y usa un registro adicional para decidir
la continuación. El cursor es opaco, contextual a usuario, tenant, recurso, sede,
incidencia, filtro y búsqueda. No concede permisos; cada página reautoriza. Fechas
ordenadas descendentes y UUID ascendente para desempatar; catálogo/personas por
nombre e ID ascendentes. La búsqueda usa subcadena literal sin comodines SQL,
y permite buscar personas por email sin devolverlo. Historial no admite búsqueda.

Los DTO son explícitos y no exponen hashes, recibos internos, email de candidatos,
claves de objeto ni cadenas de conexión. Los errores usan `errors[]`, códigos
estables y mensajes sanitizados según `API_ERRORS.md`.

## Idempotencia y trazabilidad

El hash normaliza actor, tenant, contexto y comando; fechas se comparan a
microsegundos. Una clave ya confirmada devuelve su respuesta original; cambiar
el cuerpo produce 409. `incident_events` guarda además `operation_id`,
`request_hash` y `response_body` privados para recuperar la misma operación con
otra clave, incluso después de transiciones posteriores. El primer evento usa
`clientOperationId`. El historial público solo proyecta identidad de evento,
operación, estados, nombre del actor, nota y fecha.

Se reautoriza antes de consultar ambos recibos. Crear/cambiar incidencia, acción,
evento, auditoría y recibo idempotente es una sola transacción. La auditoría y los
logs no guardan cuerpos, notas, tokens ni URLs; el cuerpo en el evento es un
registro técnico privado de idempotencia. Se conservan eventos append-only y sus
recibos mientras exista la incidencia; limpiar recibos técnicos requiere una
política posterior explícita que preserve la recuperación por operación.

## Migración y publicación

Aplicar una sola vez `V6__incidents.sql` después de V5 y antes de desplegar el
backend que exige `incidents.RequireSchema`; después publicar el frontend.
No reaplicar V1 ni publicar V3 local. No se migra producción automáticamente.
La migración añade versiones/hashes/resolución/eventos/índices; conserva reportes,
acciones y evidencia existentes. El catálogo previo queda como versión 1.

Antes de ejecutar con un operador autorizado: respaldo comprobado, V1/V2/V4/V5
registradas, catálogo sin modificaciones en curso y revisión de incidencias con
más de una acción. No eliminar ni fusionar esas acciones para superar el
preflight; requieren una decisión funcional posterior. Consultas de revisión:

```sql
SELECT organization_id, incident_id, count(*)
FROM corrective_actions GROUP BY organization_id, incident_id HAVING count(*) > 1;
SELECT organization_id, code, count(*)
FROM incident_catalog GROUP BY organization_id, code HAVING count(*) > 1;
```

Ejecutar V6 con `psql` y `ON_ERROR_STOP=1` sobre la base elegida por el operador;
la transacción revierte completa ante error. `RequireSchema` verifica columnas y
unicidad de código/versión y operación; no ejecuta migraciones al arrancar. CI
instala V1, V2, V4, V5 y V6 en su base aislada.

## Verificación

Las pruebas PostgreSQL requieren `TEST_DATABASE_URL` a una base llamada
`bpm_test`; crean y destruyen un esquema propio por prueba, sin usar public ni
producción. Cubren permisos, inactividad/revocación, sede/tenant ajenos,
privacidad WORKER, catálogo inmutable, responsable y validador distintos,
transiciones/devolución, versiones concurrentes, fotos pendientes/otra acción,
archivos no fotográficos o con varios destinos, bloqueo compartido con media, paginación contextual,
y replay por clave u operación después de cambios posteriores. HTTP cubre
entradas cerradas, UTF8 y errores sanitizados; el dominio verifica invariantes.

No se anuncia constancia/certificado ni notificaciones: corresponden al módulo
posterior de reportes y alertas.

Verificación de interfaz aislada: chef reporta y adjunta foto BEFORE, administrador
asigna al colaborador con plazo interpretado en la zona real de la sede,
colaborador inicia y registra la corrección con foto AFTER, y otro administrador
valida el cierre y consulta el historial. Fotos seleccionadas y vista previa
privada pasaron en anchos 320,390,768,1440. El cierre de galería devuelve el árbol
accesible sin pulsaciones artificiales. Firebase y el almacenamiento se simularon
solo en el arnés local; los módulos Go, HTTP, PostgreSQL y proxy fueron reales.
No se crearon cuentas reales ni se enviaron correos.

Resultado de verificación local: 35 paquetes Go con pruebas pasan con PostgreSQL
aislado y detector de carreras (`-race -count=1`); `go vet` sin errores. El contrato
OpenAPI valida 60 operaciones y 319 referencias. La frontera Node tiene 67 pruebas
sin fallos. Las pruebas de interfaz no sustituyen la aceptación con R2 real ni
una prueba en dispositivos físicos.
