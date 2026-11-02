# Especificación de seguridad

## Identidad y autorización

La política de 8 horas, inactividad, revocación y publicación está en
[SESSIONS.md](SESSIONS.md). Se aplica también a organizaciones y checklists.

- Firebase Auth administra credenciales; la API nunca recibe ni almacena
  contraseñas.
- La API valida firma, proyecto, expiración y revocación del ID token.
- El UID verificado se cruza con `application_users` y
  `organization_memberships` antes de autorizar cualquier módulo.
- Roles, tenant y sede provienen de PostgreSQL, no de campos enviados como prueba
  por el cliente.
- Registro exige email Firebase verificado; crea únicamente USER sin membresías.
  No acepta contraseñas, UID, correo ni roles en JSON. Nunca fusiona cuentas por
  correo ni modifica/rehabilita una cuenta existente al reintentar.
- Los errores HTTP usan códigos públicos y mensajes sanitizados; nunca incluyen
  el error original de Firebase, PostgreSQL u otro proveedor.

## HTTP y red

- Producción exige HTTPS en los orígenes CORS y rechaza `*`.
- Railway termina TLS y Cloudflare aporta DNS, WAF, CDN y limitación perimetral.
- La API añade HSTS y cabeceras restrictivas en `staging`/`production`.
- Las cargas multimedia no atraviesan la API; su límite JSON predeterminado es
  1 MiB.
- `HTTP_TRUST_PROXY_HEADERS=false` es seguro por defecto. Solo se habilita cuando
  el origen no puede evadir Cloudflare; de lo contrario los encabezados de IP
  pueden falsificarse.

## PostgreSQL

- Se usa `DATABASE_URL` como secreto y nunca se registra su valor.
- El pool tiene límites, tiempos de espera, vida máxima y conexiones mínimas.
- Cada conexión opera en UTC y aplica `statement_timeout`, `lock_timeout` e
  `idle_in_transaction_session_timeout`.
- En Railway, la API debe usar `${{Postgres.DATABASE_URL}}`; el TCP Proxy se
  reserva para administración o sistemas que estén fuera del proyecto.

## Evidencias R2

- Bucket privado, sin URL pública persistente.
- Claves obligatoriamente bajo `organizations/...`; se rechazan rutas absolutas,
  retrocesos y separadores no válidos.
- MIME, tamaño y SHA-256 se validan antes de firmar.
- PUT/GET firmados duran entre 1 y 15 minutos; el valor recomendado es 5 minutos.
- La aplicación móvil nunca recibe las credenciales S3/R2.
- El backend realiza HEAD y lectura acotada con MIME real, decodificación y SHA-256 de los bytes antes de marcar `media_files.status=AVAILABLE`; no confía en la metadata aportada por el cliente. Ver [MEDIA.md](MEDIA.md).

## Secretos y registros

- `.env` está excluido de Git y de la imagen Docker.
- Firebase y R2 se configuran como variables selladas en Railway.
- Logs estructurados no incluyen encabezados, cuerpos, query strings ni URLs de
  conexión.
- Sentry tiene `SendDefaultPII=false`.
- Cualquier credencial expuesta se rota desde el proveedor, no se reutiliza.

