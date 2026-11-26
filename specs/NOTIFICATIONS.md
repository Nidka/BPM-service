# Entrega externa y dispositivos privados

`features/notifications` administra los dispositivos propios y las entregas a FCM/Resend.
La bandeja y sus reglas pertenecen a [ALERTS.md](ALERTS.md). La raíz compone ambas
funcionalidades; ninguna importa la otra.

## Activación

1. Aplicar V7 después de V1/V2/V4/V5/V6, siguiendo su preflight. No ejecutar V3 local.
2. Publicar la API con las rutas nuevas y después el frontend.
3. Crear un servicio separado con la misma imagen y comando `/app/bpm-worker`.
   La API usa `/app/bpm-api`; no ejecuta el worker ni migraciones automáticamente.
4. Configurar `ALERT_WORKER_INTERVAL` (predeterminado 60s), `FCM_ENABLED=true`
   con Firebase Admin válido para push y/o `RESEND_API_KEY` + `RESEND_FROM_EMAIL`.
   Las credenciales administrativas solo viven en el servidor.
5. Verificar una regla explícita con cuentas/sedes autorizadas. Las pruebas del
   repositorio usan PostgreSQL aislado y proveedores falsos, sin envíos reales.

El worker evalúa reglas actuales y procesa `operations.receipt.recorded.v1` y
`notifications.alert.ready.v1`. Una recepción guarda su evento en la misma
transacción; rechazar el evento revierte la recepción. Reintentar la recepción
no crea otro evento ni otro aviso. El evento de aviso registra sus destinos en
una transacción; su envío ocurre después.

## Garantías y límites

- `core/outbox` solo conoce eventos técnicos. Reclama con `SKIP LOCKED`, lease
  UUID y fencing; recupera leases vencidos y finaliza intentos agotados.
- Los eventos tienen vigencia de 23h, máximo ocho intentos y espera exponencial
  acotada con jitter. Un proveedor sin configurar no consume intentos de entrega.
- Los destinatarios, membresías, rol, sede, fuente y dispositivo se vuelven a
  comprobar antes del I/O. Los cambios posteriores a esa comprobación no pueden
  retirar un mensaje que el proveedor ya aceptó.
- El destino y cuerpo de cada entrega se conservan; cada correo usa una clave
  estable `bpm-alert/<deliveryID>`. No se reenvía después de 23h: un intento
  incierto se conserva como UNCERTAIN, sin afirmar aceptación o entrega.
- SENT significa aceptación del proveedor. No significa DELIVERED, visto ni
  atendido. El dispatcher nunca cambia el estado de lectura de `alerts`.
- FCM tiene entrega al menos una vez; el cliente deduplica por alertID y verifica
  el hash del destinatario. No se promete exactamente una entrega externa.
- Los avisos push llevan texto específico por tipo (ver «Texto del push»). El correo
  conserva la invitación genérica. Ningún canal lleva observaciones, nombres de
  personas, correos, teléfonos, fotos, URLs firmadas ni tokens.
- Cada tick reconcilia hasta 100 avisos vigentes con dispositivos nuevos o tokens
  rotados. El destino se deduplica por dispositivo/hash, sin repetir el ya registrado.
- Un error tardío UNREGISTERED desactiva únicamente el dispositivo y hash del
  token usados por ese intento; nunca un token ya rotado.
- Los errores y logs conservan códigos seguros; no se registran tokens, correos,
  cuerpos de proveedores ni credenciales.

- El worker registra (nivel INFO, sin tokens, correos ni textos) `entregas del aviso preparadas` (`alertId`, canales activos y
  `fcmDevices`: cuántos dispositivos Android activos recibirán el push; `0` significa que el destinatario no tiene un
  dispositivo registrado y por tanto no hay push), `aviso sin entregas` (destinatario/fuente ya no vigente o aviso atendido)
  y `entrega de aviso procesada` (`channel`, `status` SENT/CANCELLED/FAILED/PENDING/UNCERTAIN y `code`).

Referencias oficiales: [idempotencia de Resend y ventana de 24h](https://resend.com/docs/dashboard/emails/idempotency-keys),
[envío mediante Firebase Admin](https://firebase.google.com/docs/cloud-messaging/send/admin-sdk).

## Texto del push y navegación

Al preparar la entrega FCM, `notifications/domain.BuildPush` (función pura) arma título y cuerpo con datos de
negocio resueltos por el adaptador PostgreSQL: nombre de sede, plantilla, producto/categoría, curso, medición y
plazo. Nunca incluye nombre, correo o teléfono de personas. El texto se conserva en la entrega (inmutable) y el
proveedor lo recorta por runas: título ≤ 60 y cuerpo ≤ 140, con «…». Un tipo desconocido (o una entrega preparada
antes de este cambio) usa el texto genérico «Tienes un aviso en BPM» / «Abre la aplicación para consultar tus avisos.».

| Tipo | Título | Cuerpo | `route` |
| --- | --- | --- | --- |
| `TEMPERATURE_DANGER` | `Temperatura fuera de rango · {sede}` | `{producto} ({categoría}): {t} °C` | `empresa/operacion` (ADMIN) o `control/operacion` |
| `CHECKLIST_MISSING` | `Control pendiente: {plantilla}` | `Aún no se registra el control de hoy · {sede}` | `control/programacion/{assignmentId}` |
| `ORGANIZATION_INACTIVE` | `Sin actividad reciente en BPM` | `No hay actividad desde hace {N} días. Revisa la operación.` | `empresa` |
| `TRAINING_ASSIGNED` | `Capacitación asignada: {curso}` | `{sede} · Vence el dd/MM/aaaa. Complétala en la Academia.` | `aprender` |
| `INCIDENT_ASSIGNED` | `Te asignaron una incidencia: {categoría}` | `{sede} · Plazo dd/MM/aaaa HH:mm` (hora de la sede) | `empresa/incidencias` (ADMIN) o `control/incidencias` (CHEF/WORKER) |
| otro | texto genérico | texto genérico | `avisos` |

Las partes ausentes se omiten (sin sede, sin plazo, sin medición). El mensaje FCM lleva el bloque `notification`
(título/cuerpo; Android lo muestra solo con la app en segundo plano), el canal `bpm-alerts` y la etiqueta
`alertId` para reemplazar repeticiones. `data` conserva `alertId`, `organizationId` y `recipientHash` y agrega,
cuando existen: `type` (tipo de aviso), `sourceType`, `entityId` (id de la fuente), `siteId` y `route`. `route` usa
las rutas existentes del frontend; el cliente debe validarla contra su lista de rutas y, si no la reconoce,
abrir `avisos`. Para `INCIDENT_ASSIGNED` (V21) `entityId` es el id de la incidencia, `sourceType` es `INCIDENT` y `siteId` la sede; el texto
lleva solo la categoría del catálogo, la sede y el plazo de la acción (sin nombre del reportante ni del responsable). Lo emite
`features/incidents` al asignar (ver ALERTS.md); sin V21 no se emite y el resto de avisos no cambia. Otros módulos aún no
emiten avisos externos: se agregan como un tipo nuevo en `BuildPush` cuando exista su emisor.

## API privada de dispositivos

POST `/organizations/{org}/notifications/devices` registra id UUID, installationId
UUID, platform y pushToken. La identidad se obtiene de la sesión y debe tener una
membresía activa. El mismo token puede representar al mismo usuario en varias
organizaciones; nunca a otro usuario. ID/instalación ajenos producen un conflicto
sin revelar al dueño. La respuesta solo incluye id, platform, status y lastSeenAt.

DELETE `/organizations/{org}/notifications/devices/{id}` desactiva el propio
registro y retira su token; es idempotente. No admite cuerpo ni parámetros de
consulta. No hay formularios de token en la UI.

Android solicita el consentimiento por acción explícita desde Mis avisos. iOS y
web conservan la bandeja, pero su opt-in externo no se habilita sin SDK/APNs/VAPID.
La compilación no demuestra recepción real: esa prueba requiere el servicio
worker, credenciales del operador y un dispositivo con consentimiento.
