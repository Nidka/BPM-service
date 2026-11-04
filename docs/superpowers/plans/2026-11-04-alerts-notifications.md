# Alertas y notificaciones — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans con pruebas RED/GREEN por tarea.

**Goal:** Generar avisos configurables por organización/sede y destinatario, consultarlos y atenderlos con trazabilidad, y entregar correo/push mediante outbox transaccional.
**Architecture:** `features/alerts` contiene reglas, evaluación y bandeja; `features/notifications` contiene dispositivos y entrega externa por puertos. `core/outbox` es infraestructura genérica transaccional compartida, sin reglas BPM. La raíz compone evaluador/dispatcher y conecta UI por callbacks, sin importar funcionalidades entre sí.
**Tech Stack:** Go/Chi/pgx, KMP/Compose/Ktor, PostgreSQL, Firebase Admin Messaging y Resend HTTP; Firebase Messaging Android cuando está configurado. Mantener versiones fijadas y componentes existentes.
**Spec:** FEATURES.md módulo6, Requerimientos/ResumenProyectoBPM.md RF18–22 y PlanImplementacion.md fase7. Reportes/constancias y autoría de academia siguen en sus módulos; no crear contenidos/asignaciones ficticios para activar avisos.

## Decisiones y límites

Usuario autorizó continuar directamente, reutilizar componentes/arquitectura y no detener por preguntas rutinarias. Reusar worktrees existentes, nuevas ramas sobre backend405f266/frontend382cbf9. Originales muy sucios: integración selectiva comparando base, sin reset/stash/commit global; V3 local no publicar. No enviar correos/push reales en las pruebas ni modificar credenciales/producción.

ADMIN/SUPER_ADMIN configura reglas en su organización y sedes vigentes. CHEF/WORKER no configura. Bandeja: cada usuario solo ve sus propios avisos, nunca los de otros aunque sea ADMIN. Revalidar usuario/org/membresía/rol/sede antes de lista, detalle, acuse y entrega. SUPER sin membresía no recibe alerta (FKrecipiente); puede configurar reglas, pero no consultar inbox ajeno. Alerta de sede a CHEF/WORKER exige asignación vigente; ADMIN recibe ámbitoempresa. Un cambio de rol puede retirar acceso a tipos que ya no corresponden. Dispositivos son privados del usuario; no devolver pushToken ni installationId en DTO/logs.

Tipos de regla cerrados:
- CHECKLIST_MISSING configuration {afterLocalTime:"HH:mm"}. Solo asignaciones DAILY activas de plantilla PUBLISHED y sede activa; evaluar a partir de esa hora en el día local de sede, creada la asignación antes de hoy fin. No hay alerta si existe run SUBMITTED para ese assignment y período; DRAFT no cumple. Recipientes CHEF activos de la sede. Dedup por regla+versión+assignment+inicioDía+usuario. No repetir notificaciones al completar ni inventar turnos/frecuencias que no estén implementadas.
- TEMPERATURE_DANGER configuration {minimumTemperatureCelsius:number,maximumTemperatureCelsius:number}, intervalo inclusivo, min<max, magnitudes y2decimales acordesDecimalInput existente. Ningún rango sanitario hardcodeado ni publicado automáticamente; operador/admin usa norma correspondiente. Evaluar mediciones no nulas de supply_receipt_items cuyo temp cae dentro del intervalo. Recipientes CHEF asignados+ADMIN activos. Dedup regla+versión+receiptItem+usuario. Snapshot de configuración/medición se conserva al crear alerta; no reescribir avisos existentes.
- ORGANIZATION_INACTIVE configuration {inactivityDays:integer1..365}. Última actividad servidor entre organización.created_at y recepción.created_at,limpieza.created_at,incidencia.created_at,checklist.submitted_at; ninguna fecha cliente futura ni login cuentan. Sin actividad durante N*24h, ADMIN activos reciben un aviso por episodio, dedup por regla+versión+lastActivity+usuario; no spam diario.
- TRAINING_ASSIGNED configuration {}. assignments ACTIVE de cursos PUBLISHED, target_user_id o target_role CHEF/WORKER activos con asignación a sede si assignment.location_id no null; usuario explícito y rol simultáneos deben cumplirse ambos. Creación de asignación>=creaciónRegla. No se simula nuevo contenido. Dedup regla+versión+assignment+usuario.
Regla locationId nullable (empresa); ORGANIZATION_INACTIVE siempre sin sede. TRAINING opcional sede filtra assignments de esa sede. Sin regla activa no generar avisos de ese tipo. Reglas afectan evaluaciones vigentes; avisos ya creados preservan snapshot y no cambian al editar regla. Crear y actualizar usa rowVersion+idempotencia; no eliminar. Evaluar solo recursos actuales y recipients autorizados. Al consultar/enviar un aviso revalidar fuente/alcance, sin filtrar por regla activa actual para ocultar historia propia permitida.

Bandeja: estado de lectura/atención independiente del envío. alerts.status utiliza PENDING,VIEWED,ATTENDED y CANCELLED; SENT legado es no visto si viewed_at null. Dispatcher jamás cambia status/viewed_at/attended_at al aceptar un proveedor. VIEW y ATTEND monotónicos e intrínsecamente idempotentes; ATTEND fija viewed_at si faltaba, no resuelve el recurso de negocio. Audit una vez por cambio real. No anunciar visto/atendido en UI antes de respuesta confirmada. Errores red conservan detalle y ofrecen reintento de la misma acción.

## Contrato HTTP cerrado

Prefijo /api/v1/organizations/{organizationID}; org `[A-Za-z0-9_-]{1,64}`, registros UUID, UID Firebase textual1..128puntosUnicode sin controles. Listas {data:[],pagination:{nextPageToken}}, detalle/escritura {data:Entity}. JSON estricto máximo512KiBUTF8; proxy respuestas4MiB. Errores convencionales400/401/403/404/409/422/429/500 yheaderssesión. Lista default25/max100 ycursores contextualizados user/org/recurso/sede/búsqueda/filtro. No email/token/cuerpos en logs/auditoría.

- GET /alert-rules?locationId?&query&pageSize&pageToken (soloADMIN/SUPER). locationId omiso=reglasdeempresa+todasautorizadas; presente filtra esa sede y reglasempresa? decisión: exclusivamente scopeexacto, no incluir empresa para no duplicar UI. DTO {id,locationId:null|string,locationName:"",name,type,configuration,active,emailEnabled,pushEnabled,rowVersion}.
- POST /alert-rules + Idempotency-Key input {id,locationId?,name,type,configuration,active,emailEnabled,pushEnabled}. IDs estables, nombre2..160. Crear rowVersion1.
- PUT /alert-rules/{ruleID} + Idempotency-Key input igual sinid + rowVersion. No cambiar type/locationId de regla existente: crear otra; UIpuedeeditar nombre/config/canales/active. Update DTOtrae type/locationId para comparar y rechaza diferencia. No exponerrowVersion en UI normal.
- GET /alerts?state=ALL|UNREAD|VIEWED|ATTENDED (defaultALL)&query&pageSize&pageToken. Filtro personal/autorizado SQL; DESC created_at,ID ASC.
- GET /alerts/unread-count → {count:integer>=0}; no asumir0 si error.
- GET /alerts/{alertID} → Alert.
- POST /alerts/{alertID}/view body{} → Alert.
- POST /alerts/{alertID}/attend body{} → Alert. Sin Idempotency-Key porque idempotente por naturaleza+rowlock+auditoríaúnica.
Alert DTO {id,type,title,message,status,createdAt,viewedAt?:date|null,attendedAt?:date|null,locationId?:UUID|null,locationName:"",sourceType,sourceId,sourceAvailable:boolean}. Fuente CHECKLIST_ASSIGNMENT/RECEIPT_ITEM/TRAINING_ASSIGNMENT/ORGANIZATION. ReceiptItem se resuelve a recepción al navegar desde raíz mediante endpoint propio/DTOsource: añadir sourceParentId nullableUUID (receiptID paraitem) y timezone String para sede si se entrega. Campos optional omitidos o null permitidos en lectura; inputfalsebooleanobligatorio.

- POST /notifications/devices input {id:UUID,installationId:UUID,platform:ANDROID|IOS|WEB,pushToken:string1..4096}. Solo propio usuario derivado sesión, miembro activo. ID/token existentes de otro usuario devuelven409 sin revelarlo. Rotation propia con mismoID actualiza token, no implica consentimiento de otro. Token no vuelve en respuesta.
- DELETE /notifications/devices/{deviceID} → {id,status:"INACTIVE"}; propio, idempotente; no pierde privilegios por no tener token. Respuesta dispositivo {id,platform,status,lastSeenAt?}.
Capacidad externa no es éxito: UI de optin solo si plataforma soporta; no campo de token manual ni botones de proveedor no configurado. Bandeja funciona en todas las plataformas. Androidprovider pide permiso después de acción del usuario, no en inicio; al logout limpia binding y elimina token. Push payload solo IDs y hash del UID, nunca notas/mediciones/nombres; notificación genérica y dedupe local poralertID. iOS/web requieren SDK/APNs/VAPID públicos correctos antes de habilitar su optin; no falsificar disponibilidad ni retrasar bandeja por credenciales faltantes.

## Outbox y entrega

V7 aditiva, V1/V2/V4/V5/V6 inmutables. alerts añade rule_id nullable,rule_version nullable,dedup_key nullable UNIQUE(org,dedup_key),context_snapshot JSONB,row_version default1. alert_rules row_version default1. notification_deliveries añade target_key VARCHAR128 NOTNULLdefault'legacy',target_device_idUUIDnullable,request_payload JSONBnullable,first_attempt_at,last_attempt_at,next_attempt_at,lease_tokenUUID,locked_until y UNIQUE(org,alert_id,channel,target_key) (preflightlegado). Amplía status a CANCELLED y UNCERTAIN ademásprevios. mobile_devices token_hash nullable y acceso privado; unique(token_hash) entre ACTIVE debe respetar dueño (mismoUIDmultipleorg permitido: decisión tokenhashuniqueorg por token, rejectbindingotroUID global en aplicación con lockhash). No almacenar token en outboxpayload.
Outbox añade dedup_key nullable UNIQUE(org,dedup_key),lease_tokenUUID,locked_until,expires_at. No hacer unique event_type+aggregate global sobrelegado; usar dedupkeynuevo. Índices claim/scopes. source event `operations.receipt.recorded.v1` aggregate_type supply_receipt,aggregate_id receiptID,payload {} y dedupkey `receipt:<id>`: se publica en misma transacción que nueva recepción; replay no crea otra. Rootimplementa core/outbox.Enqueue(tx,event), infrastructuregeneric; APIalertsrepo lo reutiliza para alerta+entrega pendientes atomic. Alert event `notifications.alert.ready.v1` aggregate_type alert aggregateIDalertID,payload{},dedupkey `alert:<alertID>`.

Comando separado cmd/worker, rootcompone alerts.Evaluator y notifications.Dispatcher mediante mapa dehandlers, sin featureimportaotra. Tick configurable (default60s), lotesacotados y SKIP LOCKED; leases conUUIDfencing ytimeoutsmenoreslease. Reclamar PROCESSINGvencido, no dejarlocolgado; complete/fail solo mismoLeaseToken. Backoff exponencial acotado,jitterinyectable, max8intentostotal,vidaevento<24h. Proveedor ausente difiere sin consumir presupuesto; no entrega ficticia. Falloagotado quedaFAILED/UNCERTAIN según riesgo; nunca revierte negocio. Usuario/roles/sede/fuente/dispositivo se revalidan antes de I/O, destino queda ligado al intento y token_hash: rotar token durante send no desactiva token nuevo por un error tardío del anterior.

Resendkey estable por deliveryID, endpoint fijoHTTPSapi.resend.com/emails, Idempotency-Key ypayloadinmutable porintento (to/from/asunto/cuerpo). No reintentar fuera de ventana24h de dedupe: UNCERTAIN. FCMdata-only estable{alertId,organizationId,recipientHash}; TTL/collapsekey/dedupeclient, at-least-once externo documentado, no afirmar exactlyonceFCM. SENT soloaceptaciónproveedor, DELIVERED no se inventa. TokenUNREGISTERED/DISABLED invalida solo ese registro/tokenHash y reevalúadispositivos; errorHTTPauthconfigpermanente no filtra mensajeproveedor. Sin notificaciones/correos reales en tests.

## Fronteras de implementación

- [ ] Backend alerts dominio/app/PG/HTTP/module +V7 +testsPG/evaluador +specsALERTS. Interfaz: alerts.New(pool,logger) crea Service, Service.EvaluateReceipt(ctx,org,receiptID,now)error y Service.EvaluateScheduled(ctx,now)error. Routes(pool,verifier,logger,sessions...),RequireSchema(ctx,pool). Rootnoeditar esosfiles.
- [ ] Root core/outboxinfra +features/notifications(dispositivos,dispatcher,providers),cmd/worker/config/Container/operationseventproducer. Dispatch(ctx,org,alertID,now)error recibe puertossmallproviders, jamásimportaalerts. Workerrootenqueuetypesvalidada.
- [ ] KMP features/alerts dominio/data/presentation reglas+inbox,contador yeditorrecuperable; NativepushAndroid yAPIdevicehelper port. ReusarDraftStorage,formnavegación,botones,PageNavigation. UI callbacksde fuente, no importardeotrasfeatures. RootApp/Graph/Headerrutasavisos yempresa/alertas. Mantenerborradoresal navegar; noPopupweb inaccesible.
- [ ] Proxyexplícitoalerts+devices,testnegative/publicProjection,server/Docker yOpenAPI,docs. RootpruebasUI4anchos+3builds,SuiteGoRace+JVM+Node yreviewindependiente.
- [ ] Commitslocalesycopiaselectivaoriginal; activarV7antesAPIyworker,frontenddespués. Railwayrequiereoperador; nosimular despliegue.

## Review Focus

1. Inboxprivado/adminnoaccesoajeno y revocación mientrasnotificaciónpendiente.
2. Fuentecompletada/inactivada oasignaciónrevocadaantesdelay no se entrega aviso obsoleto ni se muestran datosajenos.
3. Leasescaducados/2workers/timeoutdespuésdesend: idempotencia yestadofenced, no duplicar inbox.
4. Configuraciónfalta: noSENTfalso; actualizaciónlecturanunca serevierteporaceptaciónproveedor.
5. Díaslocales/DST/asignaciónposterior yregladepeligro configurable sinunrangoinventado.

## Registro de ejecución

- Trabajo en ramas aisladas feat/alerts-notifications, sobre backend405f266/frontend382cbf9.
- Outbox: tests RED por componentes ausentes → implementación → GREEN. Recepción+evento: RED 0eventos → GREEN1evento/replay y rollback si falla la cola.
- Dispositivos: validación RED→GREEN, PostgreSQL verifica dueño, múltiples empresas propias, rotación y cierre idempotente.
- Revisión independiente: reloj de leases fijado al tick, DELETE chunked y errores Firebase401/403 reproducidos con tests RED→GREEN. Sin envíos reales.
- Decisión: crear destinos en dispatcher, no en evaluador; la evaluación solo guarda alerta+evento. Evita mezclar reglas sanitarias y proveedores.
- Decisión: separar permiso histórico y vigencia de la fuente. Una tarea completada conserva historia propia autorizada, pero deja de enviarse como pendiente.
- Decisión: clave de regla ASCII8..100, como los proxies existentes; sin formatos incompatibles entre cliente y API.
- Pendiente durante ejecución: terminar pushAndroid, revisión del conjunto, UI4anchos y builds, integración selectiva y commits locales. No hay despliegue de esta entrega.

## Cierre técnico

- Revisión independiente del conjunto: IDs Android por usuario/empresa, invalidación observable al rotar token y vinculación atómica; acuse403 libera estado ocupado; contexto fuente aislado al cambiar usuario/empresa/rol. Pruebas cubren estas regresiones.
- Reconciliación de destinos al rotar token: RED método ausente y error de tipoSQL → GREEN con PostgreSQL real; no duplica destino ya registrado.
- Web real: fallo de color Android-only en vector reproducido → color hexadecimal multiplataforma, login/bandeja/detalle/atender y4anchos verificados sin excepciones.
- Verificación backend: go test -race -count=1 ./... (45 paquetes con tests), go vet y diffcheck sin fallos. Pruebas HTTP adicionales confirman sesión vencida en todas las rutas nuevas.
- Se documenta el orden de activación: migracionesV4/V5/V6/V7, API+worker y después UI. La petición actual autoriza subir cambios; el despliegue requiere operador con acceso a Railway.
