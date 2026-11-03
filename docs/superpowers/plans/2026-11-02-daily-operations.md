# Operación diaria: recepción, consulta PEPS y limpieza

## Fuentes y decisiones

RF-31 a RF-34 de Requerimientos/ResumenProyectoBPM.md; fase5 de
PlanImplementacion.md; specs/FEATURES.md §4; tablas V1; prototipo insumos/limpieza.
Stack existente Go/Chi/pgx, KMP/MVVM. No se publica ni migra producción aquí.

Se ejecuta directamente: el usuario ya pidió completar los módulos pendientes y
seguir documentación/componentes, sin volver a pedir permiso para cada tarea.
Se reutilizan las ramas aisladas de la entrega media, bases 6fe8022/f32d169;
el checkout original contiene cambios anteriores que se preservan.

Ruling: los prototipos resumen tres roles; FEATURES técnico añade ADMIN de empresa
y WORKER ejecutor de tareas. Aplicamos esa definición vigente: ADMIN/SUPER_ADMIN
configuran catálogos y planes; CHEF recibe insumos y consulta sus sedes; CHEF y
WORKER registran limpieza de sus sedes. WORKER no recibe acceso a recepciones,
stock, controles ni supervisión. Su nuevo destino se llama Mis tareas.

Ruling: esta entrega registra datos estructurados, consulta PEPS y limpieza con
operaciones inmutables/idempotentes. No ofrece edición de hechos confirmados,
consumo/descarte de stock, turnos PER_SHIFT ni fotos de limpieza hasta implementar
sus recorridos completos. La carga de fotos de controles sigue funcionando.
No se simulan envíos offline: los formularios y sus claves se protegen antes de
cada POST; se pueden reintentar tras restablecer conexión. Cola múltiple de nuevas
operaciones y evidencias de limpieza son siguientes incrementos explícitos.

Ruling: no usar rangos sanitarios fijos ni ejemplos como datos reales. ADMIN
configura mínimo/máximo del producto si requiere temperatura. ACCEPTED exige
temperatura dentro de ese intervalo, envase íntegro y vencimiento no anterior
al día local de recepción. REJECTED requiere motivo; OBSERVED requiere explicación
y no genera stock. Puede registrarse una recepción con varios productos.

Ruling: PEPS significa orden por receivedAt, receiptItemId, lotId. No ordenar por
vencimiento ni llamarlo PEPS. Se muestra vencido según fecha local de sede; no
se consume stock automáticamente. Lote de almacenamiento nace solo al aceptar,
con ubicación obligatoria y UUID igual al item de recepción (identidad estable).

## Contrato cerrado (prefijo /api/v1/organizations/{organizationID})

Todas las colecciones GET aceptan pageSize(1..100,25), pageToken,query(max160).
Recepciones, lotes, planes y ejecuciones requieren además locationId(UUID).
Cursores ligados a cuenta, empresa, recurso, sede y búsqueda; reautorización por página.
Lista: {data:[...],pagination:{nextPageToken}}. Detalle/POST: {data:Entity}.
POST JSON estricto, Idempotency-Key visible8..100. Campos actor/org/roles ignorados
nunca: se rechazan. No body en GET. Instant ISO8601 UTC; fechas yyyy-MM-dd.
IDs UUID; actor Firebase UID no UUID; max50 items/recepción.

- GET/POST /suppliers. Supplier: {id,name,taxId,contactName,contactPhone,contactEmail,address}.
  Nombre2..200; taxId<=20,contactName<=160(nombre personal opcional),phone<=30
  y7..15dígitos,email<=320,address<=300. Lectura ADMIN/SUPER/CHEF; alta ADMIN/SUPER.
- GET/POST /products. Product: {id,name,category,unit,requiresTemperature,minimumTemperature?,maximumTemperature?}.
  Nombre2..200,category<=100,unit1..24. Temperaturas dos decimales,abs<=9999.99;
  ambos límites presentes y ordenados si requiere; nulos si no requiere.
- GET/POST /supply-receipts; GET /supply-receipts/{receiptID}.
  Input {id,locationId,supplierId,receivedAt,documentReference,notes,clientOperationId,clientCreatedAt,items:[...]}
  Item {id,productId,batchCode,expirationDate?,quantity,temperatureCelsius?,decision,rejectionReason,packagingCondition,storageLocation,label}.
  quantity string decimal exacto3dec,>0 y<=999999999.999; batch1..100; expiry
  opcional yyyy-MM-dd; decision ACCEPTED/REJECTED/OBSERVED; envase INTACT/DAMAGED;
  reason<=2000; storageLocation1..120 siaccepted yvacío enotros; label<=160.
  receivedAt no posterior a now+5min; clientCreatedAt válido no futuro>5min.
  reference<=100,notes<=2000. Actor receivedBy derivado del token. Lectura retorna
  además supplierName,receivedBy,createdAt y por item productName,unit.
  Solo CHEF crea en sede asignada; ADMIN/SUPER leen todo; WORKER denegado.
- GET /storage-lots?locationId. Lot {id,locationId,receiptItemId,productId,productName,unit,batchCode,expirationDate?,receivedAt,storageLocation,label,quantityAvailable,status,expired}.
  quantityAvailable decimalstring; status AVAILABLE/CONSUMED/DISCARDED (lectura).
- GET/POST /cleaning-plans. Plan {id,locationId,area,task,instructions,productName,frequency,dueTime,active}.
  input sin active; se deriva true. area2..100,task2..240,instructions<=2000,
  productName<=160; frequency DAILY/WEEKLY/MONTHLY, dueTime vacío o HH:mm.
  ADMIN/SUPER crean; lectura todos autorizados por sede.
- GET/POST /cleaning-executions; GET /cleaning-executions/{executionID}.
  Input {id,locationId,cleaningPlanId,executedAt,result,observation,clientOperationId,clientCreatedAt}.
  Result COMPLETED/NOT_COMPLETED/OBSERVED. observation<=2000 y obligatoria salvo
  COMPLETED. ejecutado/clientCreatedAt <=now+5min. Responsable derivado UID.
  CHEF/WORKER propios sedes asignadas crean; ADMIN/SUPER supervisan sin ejecutar.
  Lectura añade area,task,responsibleUserId,responsibleName,createdAt.

Errores: 400 INVALID_REQUEST/INVALID_PAGINATION;401 sesión;403ACCESS_DENIED;
404NOT_FOUND;409RESOURCE_CONFLICT;422VALIDATION_ERROR con campos;429límite;
500INTERNAL_ERROR sanitizado. Se conservan headers de sesión. Lista vacía no es error.

## Tareas y fronteras

1. Dominio y casos de uso operations: validación, cantidad exacta, roles por
   acción, fecha local, recepción/stock atómicos, limpieza e idempotencia con
   auditoría. Pruebas RED/GREEN con entradas inválidas y permisos negativos.
2. PostgreSQL/HTTP/module + V5: packaging_condition, límites productos, índices
   por sede/fecha y unicidad de lote porreceiptItem. V1/V2/V4 inmutables, V3 no
   se publica. Pruebas PG aislado, replay concurrente, tenant/sede, formatos HTTP.
3. KMP feature operations domain/data/presentation y borradores recuperables:
   listas paginadas, formulario escalonado catálogos→recepción→revisión,
   plan→ejecución; sin combobox largos, reutilizar campos/teclados/botones,
   selección de sede en raíz con componente existente. Preguntas/plans no hardcodeados.
4. Proxy web explícito + pruebas negativas y Docker COPY. No redirects/cookies/
   actor enviado, respuestas públicas. Root compone VM y destinos con roles,
   limpia contexto y añade accesos desde Empresa/Control/Mis tareas.
5. OpenAPI/specs, revisión independiente, suites Go/race/PG, JVM/Node, web/Android/
   iOS, UI 320/390/768/1440 con servicios ficticios. Integración selectiva al
   original comparando base. Rama local revisable; activación externa separada.

## Revisar especialmente

Recepción aceptada dañada/vencida/fuera de límites; OBSERVED genera cero stock;
idempotencia concurrente sin duplicar recepción/lote/auditoría; IDs de proveedor/
producto/plan/sede ajenos; worker sin inventario; fecha en zona local y cursor de
otra cuenta; volver/reiniciar durante POST incierto conserva UUID/cuerpo; cambiar
empresa no restaura datos de otra; mobileIME numérico3dec y botones sin recortes.

## Ajustes del builder check antes de implementar

- Label también vacío si no es ACCEPTED; no se pierde silenciosamente un campo.
- GET products/suppliers: ADMIN/SUPER/CHEF, nunca WORKER.
- WORKER consulta únicamente sus propias ejecuciones en sedes asignadas; CHEF
  consulta ejecuciones de sus sedes; supervisores todas las sedes de su empresa.
- V5 conserva packaging nulo en legado, sin inventar envase íntegro. Nuevas altas
  exigen INTACT/DAMAGED. Listas/detalles aceptan null solo como legado de lectura.
- V5 añade minimum_temperature/maximum_temperature y row_version a products.
  No inventa límites de productos heredados: permite ambos nulos; el servicio no
  acepta recepción de un producto que requiere temperatura sin criterio definido.
  UI muestra Pendiente de configurar a ADMIN, y avisa CHEF antes de recibir.
- Vía concreta para configurar legado o cambiar criterio: PUT /products/{productID}/temperature-policy
  JSON {rowVersion,requiresTemperature,minimumTemperature?,maximumTemperature?},
  Idempotency-Key. Solo ADMIN/SUPER, control versión y auditoría; retorna Product
  con rowVersion positivo. El resto de campos de Product es inmutable en esta entrega.
- Product GET/POST respuesta incluye rowVersion; POST input no acepta rowVersion.
  ReceiptItem respuesta añade minimumTemperature?/maximumTemperature? tomados como
  snapshot al registrar, no según criterio actual. V5 conserva nulos en legado.

## Cierre de implementación y revisión

- El identificador de organización conserva `[A-Za-z0-9_-]{1,64}` del contrato
  existente; los identificadores de registros nuevos son UUID.
- Se conserva la precisión temporal de PostgreSQL al comparar la confirmación
  con el cuerpo pendiente, sin modificar el JSON original del reintento.
- Lectura de legado admite envase desconocido, código de lote vacío y UUID de
  lote independiente del ítem; las altas nuevas conservan validación estricta.
- Cuerpo máximo uniforme de 512 KiB UTF-8 en API, proxy, transporte y borrador.
  Regresión con 50 observaciones Unicode; sin límites basados en UTF-16.
- Revisiones independientes de implementación y contrato completadas, sin
  bloqueantes pendientes tras corregir precisión, legado y límites.
- Prueba UI aislada contra módulos Go y PostgreSQL reales: alta de proveedor
  como ADMIN, recepción con 1.125 kg y lote PEPS como CHEF, y limpieza como WORKER
  con catálogo de insumos denegado. Verificados anchos 320, 390, 768 y 1440.
- No se crearon usuarios Firebase reales, no se aplicaron migraciones en Railway
  ni se modificaron credenciales. El arnés ficticio está en tmp ignorado.
- Documentación de activación y preflight V5 en specs/OPERATIONS.md. Quedan fuera
  de esta entrega los movimientos de stock, turnos, fotos de limpieza y cola
  offline de nuevas operaciones, además de módulos posteriores de FEATURES.md.

Validación final 2026-11-03: 32 paquetes Go con pruebas pasan con `-race`,
PostgreSQL aislado y `-count=1`; `go vet` sin errores. JVM: 198 pruebas, cero fallos;
Node: 52 pruebas, cero fallos. Web, APK y framework iOS compilan. OpenAPI valida
50 operationId únicos y 245 referencias internas. `gofmt` y `git diff --check` limpios.
Atrás en selector/revisión mantiene el formulario en la prueba de interfaz.
