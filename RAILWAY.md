# Despliegue automático en Railway

Actualización del 18/10/2026: la API ya está publicada como BPM-service en
`https://bpm-service-production.up.railway.app`; `/health` y `/ready` respondieron
200 y la versión publicada coincide con el commit remoto `7b3f94f`. Las secciones
de creación siguientes son instrucciones de instalación inicial, no tareas por
repetir sobre el servicio existente. Los ajustes de consola (Wait for CI,
healthcheck, variables y permisos) deben confirmarse allí; no se certifican
únicamente por estar documentados.

Para la entrega de seguridad consultar [specs/SESSIONS.md](specs/SESSIONS.md):
requiere aplicar V2 antes del backend nuevo y publicar primero el frontend
compatible. V2 ya se aplicó y verificó en Railway el 18/10/2026 a las 02:20,
hora de Lima. No volver a ejecutarla. Los binarios se publican desde `main`,
primero frontend y después backend; la migración no activa por sí sola la
política de sesiones. Confirmar CI, el despliegue y la versión pública al publicar.

## Arquitectura del despliegue

```text
push o merge a main
        │
        ▼
GitHub Actions: formato + vet + tests race + build + Docker
        │
        ▼ solo si CI termina correctamente
Railway: build del Dockerfile + despliegue
        │
        ├── DATABASE_URL → Postgres privado del proyecto bpm
        └── /ready → healthcheck antes de publicar la nueva versión
```

No se usa un token de Railway dentro de GitHub Actions. Railway observa la rama
`main` mediante su integración de GitHub y la opción **Wait for CI** impide
desplegar commits cuyas pruebas fallen.

## Estado comprobado

- Proyecto Railway: `bpm`.
- Ambiente: `production`.
- Servicio existente: `Postgres`, en línea.
- Repositorio GitHub: `Nidka/BPM-service`.
- Rama de producción: `main`.
- Servicio pendiente de crear: `api`.
- El repositorio tiene `Dockerfile` en la raíz y workflow en
  `.github/workflows/ci.yml`.

La carpeta local está enlazada actualmente al servicio `Postgres`. Ese enlace
solo afecta los comandos de la CLI; la API será otro servicio dentro del mismo
proyecto.

## 1. Publicar el código en GitHub

Railway solo puede construir commits que ya existan en GitHub. Antes de crear
el servicio, confirmar los cambios y enviarlos a `main`:

```bash
cd /Users/helcorp/Downloads/Proyectos/BPM/Software/Backend
git status
git add .
git commit -m "feat: base segura del backend e identidad"
git push origin main
```

No ejecutar `git add` hasta revisar que `.env` siga ignorado:

```bash
git check-ignore .env
```

El resultado debe contener `.env`. Las credenciales nunca se suben a GitHub.

## 2. Crear el servicio API desde GitHub

En Railway:

1. Abrir el proyecto **bpm** y el ambiente **production**.
2. Pulsar **+ New → GitHub Repo**.
3. Seleccionar `Nidka/BPM-service`.
4. Nombrar el servicio `api`.
5. Seleccionar la rama `main`.
6. Mantener **Root Directory** en `/`, porque este repositorio ya tiene el
   backend y su `Dockerfile` en la raíz.
7. Si Railway solicita acceso, autorizar su GitHub App únicamente para el
   repositorio necesario.

Railway detectará automáticamente `Dockerfile`. No se configura Build Command
ni Start Command: la imagen ya compila `cmd/api` y ejecuta `/app/bpm-api`.

## 3. Conectar la API al Postgres existente

Abrir **api → Variables → New Variable → Add Reference** y elegir:

```text
DATABASE_URL = ${{Postgres.DATABASE_URL}}
```

Debe ser una referencia, no una copia de la URL. Así Railway mantiene la
conexión actualizada cuando cambien las credenciales. Esta URL usa la red
privada `postgres.railway.internal`; `DATABASE_PUBLIC_URL` queda exclusivamente
para pgAdmin u otros clientes externos.

## 4. Variables del servicio `api`

Agregar en **api → Variables**:

```env
APP_NAME=bpm-api
APP_ENV=production
LOG_LEVEL=INFO
API_BASE_PATH=/api/v1

DATABASE_URL=${{Postgres.DATABASE_URL}}
DB_POOL_MIN_SIZE=2
DB_POOL_MAX_SIZE=10
DB_CONNECTION_TIMEOUT=5s
DB_MAX_CONNECTION_IDLE=5m
DB_MAX_CONNECTION_LIFETIME=30m
DB_STATEMENT_TIMEOUT=15s
DB_LOCK_TIMEOUT=5s

HTTP_READ_HEADER_TIMEOUT=5s
HTTP_READ_TIMEOUT=15s
HTTP_WRITE_TIMEOUT=30s
HTTP_IDLE_TIMEOUT=60s
HTTP_SHUTDOWN_TIMEOUT=15s
HTTP_REQUEST_TIMEOUT=20s
HTTP_MAX_JSON_BODY_BYTES=1048576
HTTP_RATE_LIMIT_RPS=50
HTTP_RATE_LIMIT_BURST=100
HTTP_TRUST_PROXY_HEADERS=false
HTTP_HSTS_MAX_AGE_SECONDS=31536000
CORS_ALLOWED_ORIGINS=https://DOMINIO_REAL_DEL_FRONTEND

FIREBASE_ENABLED=true
FIREBASE_PROJECT_ID=PROYECTO_FIREBASE_REAL
FIREBASE_SERVICE_ACCOUNT_JSON_BASE64=SECRETO_BASE64

R2_ENABLED=true
R2_ACCOUNT_ID=CUENTA_R2_REAL
R2_ACCESS_KEY_ID=SECRETO
R2_SECRET_ACCESS_KEY=SECRETO
R2_BUCKET_NAME=BUCKET_PRIVADO_REAL
R2_ENDPOINT=https://CUENTA_R2_REAL.r2.cloudflarestorage.com
R2_SIGNED_URL_TTL=5m
MEDIA_MAX_UPLOAD_BYTES=10485760
MEDIA_ALLOWED_MIME_TYPES=image/jpeg,image/png,image/webp,application/pdf

RESEND_API_KEY=
RESEND_FROM_EMAIL=
SENTRY_DSN=
```

Reglas:

- No crear `PORT`: Railway lo inyecta automáticamente.
- No crear `APP_VERSION`: la API usa `RAILWAY_GIT_COMMIT_SHA` como versión.
- Sustituir todos los valores `REAL` y `SECRETO` antes de desplegar.
- Sellar Firebase, R2, Resend y Sentry desde el menú de cada variable.
- `APP_ENV=production` obliga a configurar Firebase y R2; si faltan, la API
  termina de forma segura y el despliegue no pasa el healthcheck.

## 5. Configurar la política de despliegue

En **api → Settings** configurar:

| Ajuste | Valor |
|---|---|
| Source branch | `main` |
| Autodeploy | habilitado |
| Wait for CI | habilitado |
| Healthcheck Path | `/ready` |
| Healthcheck Timeout | `30` segundos |
| Restart Policy | `On Failure` |
| Max Restart Retries | `10` |

No añadir todavía un **Pre-deploy Command**: el runner versionado de migraciones
aún no está implementado. La línea base `V1__baseline.sql` ya fue instalada en
el Postgres existente. Cuando exista `cmd/migrate`, se añadirá como paso previo
y un fallo de migración bloqueará el despliegue.

## 6. Publicar la API

En **api → Settings → Networking**, pulsar **Generate Domain**. Railway generará
un dominio HTTPS similar a:

```text
https://api-production-xxxx.up.railway.app
```

Verificar:

```bash
curl -i https://DOMINIO_RAILWAY/health
curl -i https://DOMINIO_RAILWAY/ready
```

Documentación desplegada:

```text
https://DOMINIO_RAILWAY/docs/
https://DOMINIO_RAILWAY/openapi.yaml
```

### Dominio administrado en Cloudflare

Cuando el dominio temporal funcione, configurar un subdominio como
`api.tudominio.com`:

1. En Railway abrir **api → Settings → Networking → Custom Domain**.
2. Escribir `api.tudominio.com`.
3. Railway mostrará un registro `CNAME` y un registro `TXT` de verificación.
4. En Cloudflare abrir **DNS** y crear exactamente ambos registros.
5. Para un subdominio de primer nivel como `api.tudominio.com`, se puede activar
   el proxy naranja de Cloudflare.
6. Si se activa el proxy, configurar **SSL/TLS** de Cloudflare en modo **Full**.
7. Esperar la verificación y probar `https://api.tudominio.com/health`.

Después, reemplazar `CORS_ALLOWED_ORIGINS` con los dominios reales del frontend.
No usar `*` en producción cuando las solicitudes incluyan credenciales.

Resultados esperados:

- `/health`: HTTP `200`, proceso vivo.
- `/ready`: HTTP `200`, PostgreSQL accesible.

El endpoint autenticado se comprueba con un ID token real de Firebase:

```bash
curl -i \
  -H "Authorization: Bearer ID_TOKEN_FIREBASE" \
  https://DOMINIO_RAILWAY/api/v1/me
```

## 7. Flujo automático posterior

Después de esta configuración inicial, cada cambio sigue este flujo:

1. Se crea una rama y un Pull Request.
2. GitHub Actions valida Go y construye la imagen Docker.
3. Se fusiona el Pull Request en `main`.
4. GitHub Actions vuelve a validar el commit de `main`.
5. Railway espera el resultado por **Wait for CI**.
6. Si CI aprueba, Railway construye el `Dockerfile` y despliega.
7. Railway consulta `/ready`; solo publica la versión si responde correctamente.
8. Si CI o el healthcheck fallan, la versión anterior permanece activa.

## Diagnóstico

```bash
railway status
railway service list
railway logs --service api
```

Si el build funciona pero el despliegue falla, revisar primero:

1. variables Firebase y R2 obligatorias;
2. referencia exacta `${{Postgres.DATABASE_URL}}`;
3. logs de conexión PostgreSQL;
4. respuesta de `/ready`;
5. que el servicio esté conectado a `main` y tenga **Wait for CI** activo.

## Seguridad

- Solo `api` recibe dominio HTTP público; Postgres permanece privado.
- El TCP Proxy público de Postgres se deshabilita cuando pgAdmin ya no lo
  necesite.
- No se copian variables desde `.env` local ni se suben secretos a GitHub.
- Las credenciales que hayan sido expuestas en capturas o conversaciones deben
  regenerarse antes del despliegue de producción.
- Se recomienda exigir 2FA en GitHub y Railway y proteger la rama `main` para
  requerir el workflow **Backend CI** antes del merge.
