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
- Los avisos externos solo contienen una invitación genérica a abrir BPM y sus
  identificadores. No llevan observaciones, temperaturas, nombres ni fotos.
- Cada tick reconcilia hasta 100 avisos vigentes con dispositivos nuevos o tokens
  rotados. El destino se deduplica por dispositivo/hash, sin repetir el ya registrado.
- Un error tardío UNREGISTERED desactiva únicamente el dispositivo y hash del
  token usados por ese intento; nunca un token ya rotado.
- Los errores y logs conservan códigos seguros; no se registran tokens, correos,
  cuerpos de proveedores ni credenciales.

Referencias oficiales: [idempotencia de Resend y ventana de 24h](https://resend.com/docs/dashboard/emails/idempotency-keys),
[envío mediante Firebase Admin](https://firebase.google.com/docs/cloud-messaging/send/admin-sdk).

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
