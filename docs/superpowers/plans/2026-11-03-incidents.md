# Incidencias y acciones correctivas — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans para implementar este contrato por tareas con pruebas RED/GREEN.

**Goal:** Permitir reportar, asignar, atender y validar incidencias por sede con evidencia privada antes/después y trazabilidad.
**Architecture:** Recorrido `features/incidents` domain/application/adapters en Go y domain/data/presentation en KMP. `features/media` amplía su destino explícito para reutilizar cargas y vistas previas; no se simulan preguntas de control ni se importa una feature desde otra. La raíz conecta ambos módulos.
**Tech Stack:** Go/Chi/pgx, KMP/Compose/Ktor, PostgreSQL, R2 ya configurados; ninguna dependencia nueva.
**Spec:** Requerimientos/ResumenProyectoBPM.md RF35–38, PlanImplementacion.md fase6, backend specs/FEATURES.md sección5, ModeloDatos.md. RF39 constancia y notificaciones se mantienen para su módulo de reportes; no se anuncia certificado inexistente.

## Restricciones y decisiones

El usuario autorizó continuar directamente con los módulos y documentación existente; sin otra ronda de diseño. Reusar los worktrees actuales, nuevas ramas sobre backend163a7d9/frontenda5a55b1. No tocar checkout original hasta comparación selectiva final; no publicar V3 local ni migrar producción.

Permisos: ADMIN/SUPER configura catálogo y supervisa; CHEF reporta y supervisa sus sedes; WORKER reporta y atiende acciones que le asignaron en sus sedes, solo ve incidencias que reportó o le asignaron. Asignar/verificar/devolver: CHEF/ADMIN/SUPER; START/RESOLVE: únicamente responsable. Nadie valida su propia corrección, aunque sea administrador/chef. Objetivo asignable: CHEF o WORKER activo, membresía y sede vigentes. ADMIN no se hace pasar por chef ni ejecutor. Todas las consultas reautorizan usuario/org/membresía/sede, con bloqueos FOR SHARE en transacción de escritura. No nombres/sedes/criterios oficiales de prueba en producción.

Catálogo: versiones inmutables agrupadas por código, versión1 al crear; siguiente versión desde la última con expectedVersion. Un incidente referencia un catalogID inmutable. Listar solo la última versión por código; no sobrescribir criterio previo. Crear/versión solo supervisor ADMIN/SUPER. Código nuevo uppercase [A-Z0-9_-]{1,40}; nombres2..200,área2..100,criterio1..4000,risk LOW/MEDIUM/HIGH/CRITICAL,evidenceRequired boolean obligatorio.

Incidencia nueva: OPEN. Un plan correctivo por incidencia nueva; ASSIGN → ASSIGNED, START → IN_PROGRESS, RESOLVE → RESOLVED, VERIFY → CLOSED con acción VERIFIED y verifiedBy/At. RETURN desde RESOLVED → IN_PROGRESS exige motivo; conserva evento inmutable. No transición libre ni borrado. ASSIGN requiere responsable/plazo futuro y descripción1..2000. RESOLVE explicación1..2000; VERIFY/RETURN nota1..2000. Datos de responsable/actor vienen del servidor. Cerrar requiere otra persona y, si criterio lo exige, ≥1 foto AVAILABLE BEFORE y ≥1 AVAILABLE AFTER vinculada a esa acción, misma sede/tenant. Media pendiente/fallida no cuenta. Los casos anteriores de múltiples acciones no se cierran de forma automática ni se descartan: detalle conserva acciones; comandos que requieren acción única rechazan conflicto sin cambiar datos.

V6 aditiva: incident_catalog.version_number default1 positivo, sustituir unique(org,code) por unique(org,code,version_number); incidents.row_version default1,request_hash nullablehex64; corrective_actions.resolution_note nullable. Nueva incident_events append-only para operaciónUUID,estado origen/destino,actor,nota y hora con unique(org,operationID),FKs; índices de paging. V1/V2/V4/V5 inmutables. Migración y preflight documentados, RequireSchema en arranque, CI instala V6 trasV5.

## Contrato HTTP cerrado

Prefijo /api/v1/organizations/{organizationID}; organización [A-Za-z0-9_-]{1,64},registros UUID. Lista `{data:[],pagination:{nextPageToken}}`; detalle/escritura `{data:Entity}`. Inputs estrictos, máximo512KiB UTF8, responseproxy4MiB. Idempotency-Key8..100visible en POST. Errores400INVALID_REQUEST/INVALID_PAGINATION,401UNAUTHENTICATED/SESSION_EXPIRED,403ACCESS_DENIED,404NOT_FOUND,409RESOURCE_CONFLICT,422VALIDATION_ERRORcampos,429,500INTERNAL_ERROR sanitizados. Headers sesión existentes conservados.

- GET /incident-catalog?pageSize&pageToken&query → últimas versiones, todos rolesautorizados.
- POST /incident-catalog input {id,code,name,area,normativeCriterion,riskLevel,evidenceRequired} → Catalog.
- POST /incident-catalog/{catalogID}/versions input {id,expectedVersion,name,area,normativeCriterion,riskLevel,evidenceRequired} → Catalog; hereda code,version+1.
Catalog response {id,code,name,area,normativeCriterion,riskLevel,evidenceRequired,versionNumber}.
- GET /incidents?locationId&pageSize&pageToken&query&status? → IncidentSummary paginada, filtro visibilidad WORKER en SQL. statusenum optional. order createdAt DESC,id.
- POST /incidents input {id,locationId,catalogId,description,clientOperationId,clientCreatedAt}. descripción1..4000,fecha<=now+5min. → IncidentDetail.
- GET /incidents/{incidentID} → IncidentDetail.
- GET /incidents/{incidentID}/assignees?pageSize&pageToken&query → {id,displayName,role}; solo supervisores, elegibles de la misma sede. Búsqueda por nombre/email sin devolver email.
- POST /incidents/{incidentID}/actions input {operationId,rowVersion,actionType,actionId?,responsibleUserId?,description?,dueAt?,note?}. ASSIGN exige actionIdUUID, responsable,desc,plazo; otros solo operationID,rowVersion,actionType,note obligatorio salvoSTART. Rechazar campos que no corresponden al comando. → IncidentDetail.
- GET /incidents/{incidentID}/history?pageSize&pageToken → Event,autorizado,ordenfechaDESC/id.
IncidentSummary {id,locationId,catalogId,name,area,riskLevel,status,description,reportedByName,createdAt,rowVersion}.
IncidentDetail amplía {reportedBy,catalogVersion,normativeCriterion,evidenceRequired,closedAt?,actions:Action[],allowedActions:String[],beforeCount,afterCount}.
Action {id,responsibleUserId,responsibleName,description,dueAt,status,resolutionNote,resolvedAt?,verifiedAt?,verifiedBy?}.
Event {id,operationId,fromStatus,toStatus,actorName,note,createdAt}; no hash ni cuerpos de JSON ni tokens.
allowedActions personalizados: ASSIGN,START,RESOLVE,VERIFY,RETURN. Visible no sustituye autorización.

Idempotencia: UUID+operationID/key/hash normalizado actor/contextocomando. Misma clave/cuerpo devuelve respuesta original, otra carga conflicto; con otra clave pero mismo operationID consulta evento/registro existente y hash confirmado para recuperar resultado perdido. Reautorizar antes de replay. Cambio concurrente rowVersion rechaza409 y nunca finge confirmar. Incidencia+acción+evento+auditoría+record idempotente en unatransacción. Fechas comparar microsegundos, mantenerJSONpendienteintacto.

## Fotos privadas: contrato compatible

Reusar POST /media/uploads con exactamente un destino: checklist {runId,itemId} **o** incidente {incidentId,actionId?,stage:BEFORE|AFTER}; AFTER exige actionId y BEFORE lo omite. Otros metadata existentes id,originalName,mimeType,sizeBytes,sha256. FileDTO conserva runId/itemId (vacíos para incidente) y añade incidentId?,actionId?,stage? solo incidentes. GET /incidents/{incidentID}/evidence/BEFORE y GET /incidents/{incidentID}/actions/{actionID}/evidence/AFTER devuelven listingmediahabitual. Confirm/download/remove comparten endpoints previos y verificación SHA/MIME/bytes reales, mismo límite3porstage/acción.

Lectura fotos: supervisores de sede o reportero/responsable autorizado. BEFORE escritura por reportero o supervisor mientras OPEN/ASSIGNED/IN_PROGRESS. AFTER solo responsable cuando incidencia y acción IN_PROGRESS. Ni upload/remove al resolver/cerrar; sin falsificar evidenciaantesni reutilizar archivo deotroincidente. Media toma bloqueo de incidente como hace hoy con revisión; VERIFY cuenta solo fotos AVAILABLE bajo bloqueo del mismo incidente. No URL pública. No duplicar adaptador R2.

## Tareas y fronteras

- [x] Backend incidents dominio/app/repositorio/HTTP/module,V6,PGtests y contratoOpenAPI. Separar modelos/reglas/comandos/lecturas/repositorios según responsabilidad. Pruebas RED/GREEN: transiciones,roles,autovalidación,tenant/sede,plazo,versiones,privacidadworker,fotos pendientes y replayconcurrente.
- [x] Media Go y KMP: destino explícito incidente, mantener100% compatibilidad de controles; tests targetinválido,cruceincidente/acción y cierre, selección/vista previa/retryexistentes.
- [x] KMP incidents: catálogo y autoría ADMIN, reportar, listas por sede/filtroestado, detalle con siguienteacción humana, selector paginado de responsable, revisión antes de enviar, explicación requeridasegúncomando e historial paginado. Reusarcampos/teclados/botones y DraftAutosave/JsonDraftDocument; guardar UUID/key/body antesHTTP, reintentar mismoresultado, protegersalidas/sesión. UI noimportamedia, raízinyecta callbackspara fotos.
- [x] Proxy incidents explícito proyección/whitelist/validación/pruebas; ampliar proxy media compatible, DockerCOPY. Rootconecta rutas porrol/sede y callbacksmedia.
- [x] Revisióndiseño e implementación, suitesGo/race/PostgreSQL,JVM/Node y compilacioneswebAndroidiOS; UIaislada320/390/768/1440; docs,commitslocales,copiaselectiva original. Publicación exigeV6antesdelbackend y despuésfrontend; no migrarproducciónsinoperador.

## Review Focus

1. Otro usuario inicia sesión y hereda formulario o sugerencias: contextoreautorizar y aislar cuenta/org/sede.
2. RespuestaPOSTperdida/clave nueva: recuperar mismohecho sin duplicarplan/evento.
3. Ejecutor también supervisor: nunca valida su acción, aunque tenga rol elevado.
4. Foto pendiente ootraacción no permite cierre; carrerasremove/verifybloqueanincidente.
5. Criterioactualizadodespuésdereporte: historialyreglasderivanversiónreferenciada.

## Registro de revisión y decisiones de ejecución

- Revisión independiente reprodujo y corrigió asociaciones multimedia ambiguas,
  sede física distinta al destino y cambio de rol del responsable a ADMIN.
  Regresiones Go observaron RED y GREEN con PostgreSQL aislado.
- La lectura histórica admite VERIFIED, CANCELLED, más de 100 acciones, fotos
  anteriores al límite actual y UID Firebase textual. Las nuevas cargas siguen
  limitadas a tres; las reglas nuevas no se aplican como descarte de datos antiguos.
- Rechazos HTTP definitivos liberan pending y conservan el formulario editable;
  los resultados inciertos mantienen exactamente UUID, clave y cuerpo. El proxy
  permite recuperar una asignación ya confirmada aunque su plazo haya pasado;
  el caso de uso valida futuro solo al ejecutar una asignación nueva.
- La API de ubicaciones ya devolvía timezone; se conserva en el modelo KMP para
  interpretar el plazo en la sede, sin asumir America/Lima en la UI.
- Se retiró el GET resumido de incidencias del router de organizaciones para
  que el nuevo módulo sea dueño único del endpoint. Workspace conserva sus
  resúmenes iniciales compatibles; los accesos UI llevan al recorrido completo.
- Ruling: fotos web usan Surface en el árbol principal y ocultan semántica del
  fondo mientras están abiertas. ModalBottomSheet/Dialog al cerrarse provocaba
  desaparición reproducible del árbol AX, aunque el contenido visual continuaba
  intacto. Android/iOS/JVM mantienen ventanas nativas mediante un puerto técnico
  mínimo en core/presentation, reutilizado por fotos de controles e incidencias.

Aceptación local completa: reporte CHEF con BEFORE y preview privado, asignación
ADMIN con zona horaria real, atención y AFTER por WORKER sin autovalidación,
validación ADMIN distinta y CLOSED con historial. Anchos de reporte/galería
320/390/768/1440. Los servicios Go y PostgreSQL fueron reales; identidad y objetos
se sustituyeron solo dentro de tmp ignorado. Se corrigió además la proyección
web de timezone y se separó CreateLocationDto del DTO de lectura para no romper
el alta de sedes con encodeDefaults=true. Regresiones observaron RED y GREEN.

Verificación final del 3 de noviembre de 2026: 35 paquetes Go con pruebas en
PostgreSQL aislado pasan con -race -count=1, go vet limpio; 226 pruebas JVM
sin fallos ni omitidas; 67 pruebas Node; compilaciones Wasm, APK Android y
framework iOS aprobadas. OpenAPI: 60 operationId únicos,319 refs resueltas.
La aceptación también comprobó lectura histórica WORKER de incidencia cerrada,
galería sin acciones de edición y restitución accesible después de cerrar fotos.
No hubo cuentas, correos, R2 ni migraciones de producción en esta aceptación.
