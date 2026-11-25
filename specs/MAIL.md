# Correo transaccional propio

Firebase (plan Spark) no permite editar la plantilla ni la URL de acción de sus correos
(`EMAIL_TEMPLATE_UPDATE_NOT_ALLOWED`), así que sus enlaces abren una página genérica. BPM envía por sí mismo los correos
de **verificar correo** y **crear nueva contraseña** con la API de Resend, y los de **invitación a una organización**.
Implementación: `internal/features/mail` (+ `internal/core/integrations/resend`).

## Endpoints

| Ruta | Acceso | Éxito |
|---|---|---|
| `POST /api/v1/auth/email/verification` | ID token Firebase (aunque el correo aún no esté verificado) | 202 `{"data":{"status":"SENT"}}` o `ALREADY_VERIFIED` |
| `POST /api/v1/auth/email/password-reset` | Pública, cuerpo `{"email":"…"}` | 202 `{"data":{"status":"ACCEPTED"}}` |

- El destinatario de la verificación sale **siempre del token**; el cliente no elige correo ni cuerpo.
- `password-reset` responde 202 exista o no la cuenta (Firebase Admin devuelve `EMAIL_NOT_FOUND` y no se envía nada). Un
  correo con formato inválido es 422 `VALIDATION_ERROR` (no depende de si existe).
- Límites: por IP 5 en ráfaga y 1 cada 5 s; por cuenta (UID) 3 verificaciones cada 10 min; por correo 3 recuperaciones cada
  15 min. Exceso: 429 `RATE_LIMITED` con `Retry-After`. Los contadores son en memoria por réplica (guardan un hash, no la dirección).
- Limitación conocida: durante una caída de Resend, 503 aparece solo para correos con cuenta; además, enviar tarda más que
  no enviar. Ambas diferencias quedan acotadas por los límites anteriores.

## Enlaces

`GenerateEmailVerificationLink` / `PasswordResetLink` (Firebase Admin) producen
`https://<proyecto>.firebaseapp.com/__/auth/action?mode=…&oobCode=…&apiKey=…`. Solo se conservan `mode` y `oobCode` y se
construye `{APP_PUBLIC_URL}/auth/accion?mode=verifyEmail|resetPassword&oobCode=…&lang=es`, página que ya resuelve la app.
Un enlace sin el modo pedido o con un código de forma inválida se descarta (el cliente usa el respaldo).

## Respaldo a Firebase (contrato con el cliente)

Si no se puede enviar (sin `RESEND_API_KEY`, Resend responde error/429/5xx/timeout, Firebase Admin no está disponible o el
enlace es irreconocible) la API responde **503 `MAIL_UNAVAILABLE`**. El cliente debe usar entonces
`accounts:sendOobCode` de Firebase, como antes. El cliente también usa el respaldo si la ruta no existe (404/405, backend
anterior) o hay un fallo de red hacia la API; 429 y 422 **no** activan el respaldo. La API arranca y funciona sin
`RESEND_API_KEY`: todas las rutas existen y responden `MAIL_UNAVAILABLE`.

## Invitaciones

El worker (`bpm-worker`) sigue enviando la invitación desde el outbox con la misma clave de idempotencia, pero ahora con
la plantilla HTML de marca (más texto alternativo) y el mismo remitente/reply-to cuando hay `INVITATION_ACCEPT_URL`. Sin
esa URL se conserva el texto plano heredado con el token. **El worker necesita las mismas variables de correo que la API.**

## Variables

| Variable | Ejemplo | Notas |
|---|---|---|
| `RESEND_API_KEY` | `re_xxxxxxxx` | Secreto. Vacía = sin correo propio (respaldo a Firebase). |
| `MAIL_FROM` | `BPM <noreply@appbpm.com>` | Por defecto ese valor; `RESEND_FROM_EMAIL` se acepta como alias heredado. El dominio debe estar verificado en Resend. |
| `MAIL_REPLY_TO` | `ayuda@appbpm.com` | Opcional. |
| `APP_PUBLIC_URL` | `https://appbpm.com` | Origen de la app (sin ruta); por defecto `https://appbpm.com`. Define enlaces, mascota y pie. |

## Plantillas

`domain/templates.go`: HTML en español con tablas, estilos en línea, cabecera `#005B59`, tarjeta redondeada, botón grande,
Baloo 2 / Nunito con familias del sistema de respaldo, modo oscuro (`prefers-color-scheme` y `[data-ogsc]`), preencabezado
oculto y alternativa de texto plano. La mascota se sirve desde la propia app:
`{APP_PUBLIC_URL}/composeResources/pe.bpm.resources/drawable/mascot_{ready,fix,class}.png`. Todo texto variable se escapa y
solo se aceptan enlaces http(s).

## Seguridad

Nunca se registran ni se devuelven enlaces, códigos `oobCode`, cuerpos de correo, claves ni direcciones; los errores del
proveedor se reducen a sentinelas. La clave de Resend solo viaja a `https://api.resend.com/emails` (sin seguir redirecciones).
