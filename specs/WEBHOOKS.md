# Webhooks salientes (RF-48, integración con n8n)

El ADMIN de una empresa registra hasta 5 direcciones HTTPS públicas (por ejemplo, un nodo *Webhook* de n8n) y
elige qué avisos recibe: `CHECKLIST_MISSING`, `TEMPERATURE_DANGER`, `ORGANIZATION_INACTIVE`,
`TRAINING_ASSIGNED`. Requiere V18.

## Entrega

Cuando el evaluador crea una alerta, encola `webhooks.alert.v1` en el outbox (una vez por episodio, no por
destinatario) solo si la empresa tiene un webhook activo suscrito. El worker la envía por `POST` con:

| Encabezado | Valor |
|---|---|
| `X-BPM-Event` | Tipo de aviso (`TEMPERATURE_DANGER`, … o `TEST`) |
| `X-BPM-Delivery` | Identificador estable de la entrega; úsalo para descartar duplicados en reintentos |
| `X-BPM-Timestamp` | Segundos Unix del envío |
| `X-BPM-Signature` | `sha256=` + HMAC-SHA256 hex de `timestamp + "." + cuerpo` con el secreto del webhook |

Cuerpo (sin datos personales de quién recibe la alerta):

```json
{"id":"…","type":"TEMPERATURE_DANGER","organizationId":"…","locationId":"…","title":"…","message":"…",
 "sourceType":"…","sourceId":"…","occurredAt":"2026-11-21T14:00:00Z"}
```

Una respuesta fuera de 2xx, un error de red o más de 10 s se reintentan con espera exponencial (máximo 8
intentos, 23 h). No se siguen redirecciones.

## Seguridad

- Solo `https://` a nombres públicos; se rechazan IP y nombres internos al guardar y, al conectar, toda IP
  resuelta privada, local, de enlace o reservada (protección SSRF).
- El secreto (`whsec_…`) se muestra solo al crear o rotar. Rotarlo invalida el anterior al instante.
- Verificación en n8n (nodo *Code*): recalcular el HMAC con el secreto sobre `timestamp.cuerpo`, comparar con
  `X-BPM-Signature` y rechazar marcas de tiempo de más de 5 minutos.

## API

`GET|POST /organizations/{org}/webhooks`, `PUT|DELETE /organizations/{org}/webhooks/{id}`,
`POST …/{id}/rotate-secret` y `POST …/{id}/test` (envía un evento `TEST` y devuelve `{delivered, status}`).
