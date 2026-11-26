# Backend BPM

Base del backend BPM implementada en Go como monolito modular con arquitectura
hexagonal, Clean Architecture y dependencias orientadas por interfaces.

Repositorio: https://github.com/Nidka/BPM-service · Frontend: https://github.com/Nidka/BPM-frontend ·
Producción: https://appbpm.com

## Guía rápida de operación

### Qué es y con qué está hecho

API REST (`/api/v1`) y worker de avisos de la plataforma BPM (Buenas Prácticas de Manipulación).

| Pieza | Tecnología |
|---|---|
| Lenguaje | Go 1.27 (monolito modular, un solo binario `cmd/api`) |
| Base de datos | PostgreSQL 18 (`pgx`), migraciones SQL versionadas en `migraciones/` |
| Identidad | Firebase Auth (tokens verificados con Firebase Admin) |
| Archivos | Cloudflare R2 privado (URL firmadas) |
| Correo | Resend (opcional; sin clave el cliente usa Firebase) |
| Notificaciones push | Firebase Cloud Messaging (desde el worker) |
| Observabilidad | logs JSON, `/health`, `/ready`, Sentry opcional |

### Ejecutar en local

```bash
docker compose up -d postgres      # PostgreSQL local (puerto 5433)
cp .env.example .env               # ajustar si hace falta; .env no se versiona
go run ./cmd/api                   # API en http://localhost:8080  (docs: /docs/)
# o con recarga automática: make dev
```

Verificación antes de integrar (las mismas puertas que CI):

```bash
gofmt -l cmd internal              # no debe listar nada
go vet ./...
go test -race ./...
```

Algunas pruebas de PostgreSQL solo corren con `TEST_DATABASE_URL` apuntando a una base **vacía y aislada** llamada
`bpm_test` (nunca producción). Sin la variable se omiten. Para ejecutarlas en paralelo entre paquetes, aplicar antes
`V1`, `V2`, `V4`..`V7` sobre esa base como hace `.github/workflows/ci.yml`. La base de pruebas debe tener `LC_CTYPE`
UTF-8: con `C`/`POSIX` la prueba `TestDailyOperationsAllKeysetPagesAndLiteralQueries` se omite (ver
[specs/MIGRATIONS.md](specs/MIGRATIONS.md)).

### Variables de entorno (solo nombres; los valores viven en Railway)

El contrato completo y comentado es `.env.example`. Las **secretas** (variables selladas en Railway) están marcadas.

| Grupo | Variables |
|---|---|
| Aplicación | `APP_NAME`, `APP_ENV` (`production` exige Firebase y R2), `APP_VERSION`, `APP_HOST`, `PORT`, `API_BASE_PATH`, `LOG_LEVEL` |
| HTTP / CORS | `CORS_ALLOWED_ORIGINS`, `HTTP_*` (timeouts, límite de cuerpo, rate limit, HSTS, `HTTP_TRUST_PROXY_HEADERS`) |
| Sesiones | `AUTH_SESSION_ABSOLUTE_TIMEOUT`, `AUTH_SESSION_IDLE_TIMEOUT` |
| PostgreSQL | `DATABASE_URL` (secreta; en Railway usar la referencia `${{Postgres.DATABASE_URL}}`), `DB_*` |
| Firebase | `FIREBASE_ENABLED`, `FIREBASE_PROJECT_ID`, `FIREBASE_SERVICE_ACCOUNT_JSON_BASE64` (secreta) |
| R2 | `R2_ENABLED`, `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID` (secreta), `R2_SECRET_ACCESS_KEY` (secreta), `R2_BUCKET_NAME`, `R2_ENDPOINT`, `R2_SIGNED_URL_TTL`, `MEDIA_*`, `TRAINING_VIDEO_MAX_BYTES` |
| Avisos y correo | `ALERT_WORKER_INTERVAL`, `FCM_ENABLED`, `RESEND_API_KEY` (secreta), `MAIL_FROM`, `MAIL_REPLY_TO`, `APP_PUBLIC_URL`, `INVITATION_ACCEPT_URL` |
| Proceso | `BPM_PROCESS` (`worker` solo en el servicio del worker) |
| Observabilidad | `SENTRY_DSN` (secreta, opcional) |

Nunca se confirman valores reales en Git ni se copian a la app móvil.

### Despliegue (Railway)

Proyecto Railway `bpm`, ambiente `production`. Cada push a `main` ejecuta GitHub Actions (formato, vet, pruebas con
race y base PostgreSQL efímera) y Railway despliega solo si CI termina bien (*Wait for CI*). Servicios:

| Servicio | Origen | Notas |
|---|---|---|
| `BPM-service` (API) | este repositorio, `Dockerfile` | healthcheck `/ready`; `APP_ENV=production` |
| `bpm-worker` | **la misma imagen** de este repositorio | variable `BPM_PROCESS=worker`; mismas variables que la API (DB, Firebase, correo, `FCM_ENABLED`, `ALERT_WORKER_INTERVAL`) |
| `BPM-frontend` | repositorio `BPM-frontend` | sirve la web y hace de proxy cerrado hacia la API |
| `Postgres` | plantilla de Railway | PostgreSQL 18, red privada |

Servicios externos: Cloudflare R2 (archivos y videos), Firebase (Auth + FCM), Resend (correo desde `appbpm.com`),
Cloudflare (DNS y proxy). Detalle y primeros pasos en [RAILWAY.md](RAILWAY.md).

### Migraciones (V1..V20)

Las migraciones son SQL manuales hacia adelante, nunca se editan una vez aplicadas (reglas en
[specs/MIGRATIONS.md](specs/MIGRATIONS.md)). Producción está en **V20**. Para una base existente:

```bash
export DATABASE_URL=...                     # o dejarla en .env
./scripts/aplicar-migraciones.sh --revisar  # solo muestra qué falta (V15..V20)
./scripts/aplicar-migraciones.sh            # pide confirmación, hace pg_dump en backups/ y aplica
```

(`aplicar-migraciones.ps1` es el equivalente para Windows.) Aplicar **antes** de desplegar la API que las necesita:
la API falla al arrancar si falta una migración requerida. En una base nueva se aplican `V1`..`V20` en orden con
`psql -v ON_ERROR_STOP=1 -f`. Haga siempre un respaldo (`pg_dump --format=custom`) antes.

### Catálogo de cursos y banco de preguntas

Instala o actualiza en **una** organización los cinco módulos BPM y el banco oficial de 100 preguntas (idempotente;
por defecto solo simula). Las organizaciones nuevas lo reciben automáticamente al crearse.

```bash
DATABASE_URL=... go run ./cmd/catalogo-bpm -org <id-organizacion>            # simulación
DATABASE_URL=... go run ./cmd/catalogo-bpm -org <id-organizacion> -aplicar   # guarda
railway run go run ./cmd/catalogo-bpm -videos   # registra y verifica en R2 los 5 videos de plataforma (requiere R2_*)
```

### Limpieza de datos de prueba

`scripts/limpiar-organizacion.sql` borra por completo una organización de prueba y sus dependencias en una sola
transacción. Sin `confirmar=SI` solo muestra lo que borraría (ROLLBACK):

```bash
psql "$DATABASE_URL" -v org_name='Nombre exacto' -v emails='a@x.com,b@y.com' -v confirmar=NO -f scripts/limpiar-organizacion.sql
```

Haga un `pg_dump` antes y no lo ejecute sobre datos reales sin revisar la salida de simulación.

### Documentación

[Índice de especificaciones](specs/README.md) · [Arquitectura](specs/ARCHITECTURE.md) ·
[Operación](specs/OPERATIONS.md) · [Seguridad](specs/SECURITY.md) · [Correo](specs/MAIL.md) ·
[Errores de la API](specs/API_ERRORS.md) · [OpenAPI](specs/OPENAPI.md) · [Railway](RAILWAY.md) ·
[AGENTS.md](AGENTS.md) (convenciones para quien continúe el desarrollo) · planes históricos en `docs/`.
Los manuales de usuario y el manual técnico (PDF) están en el repositorio de documentación del proyecto.

---

## Detalle de la base de código

## Estructura

- `cmd/api`: punto de entrada mínimo.
- `internal/core`: configuración, ciclo de vida, inyección manual,
  infraestructura HTTP/PostgreSQL, seguridad e integraciones compartidas.
- `internal/features`: módulos verticales del negocio con `domain`,
  `application` y `adapters`.
- `internal/shared`: primitivas compartidas y utilidades puras cuando exista
  reutilización real.
- `migraciones`: línea base y cambios incrementales de PostgreSQL.
- `specs`: decisiones y especificaciones para continuar el desarrollo.

`shared/utils` está reservado y gobernado: no recibe reglas de negocio ni
dependencias externas y solo se usa para eliminar duplicación comprobada.

## Componentes disponibles

- Configuración tipada y validada desde variables de entorno.
- PostgreSQL mediante `pgxpool`, límites de conexiones y timeouts de seguridad.
- Servidor HTTP con apagado controlado y endpoints `/health` y `/ready`.
- OpenAPI 3.0.3 en `/openapi.yaml` y Swagger UI embebida en `/docs/`.
- Request ID, logs estructurados, recuperación de pánicos y soporte opcional de
  Sentry sin PII predeterminada.
- CORS por allowlist, HSTS, cabeceras restrictivas, rate limit y límite de cuerpo.
- Verificación de ID tokens revocados mediante Firebase Admin.
- Feature de identidad con `GET /api/v1/me`, usuario, organizaciones, roles y
  locales activos obtenidos desde PostgreSQL.
- Feature de organizaciones con selección de espacio de trabajo, alta de
  organizaciones/sedes y adición de miembros existentes según rol y tenant.
- Feature de checklists con publicación inicial, catálogo descargable,
  borradores offline, respuestas idempotentes y envío con puntaje del servidor.
- Feature de Academia BPM con cursos versionados, videos privados MP4/WebM,
  modo estudio, intentos calificados en servidor y constancias (V8).
- Errores HTTP uniformes mediante un arreglo `errors`, códigos estables,
  mensajes seguros, campos inválidos y `requestId`.
- Adaptador Cloudflare R2 privado con PUT/GET firmado, MIME, tamaño, SHA-256 y
  validación de claves bajo `organizations/...`.
- Contenedor de producción sin shell y ejecutado con usuario no privilegiado.

Documentación para continuar el proyecto:

- [Índice de especificaciones](specs/README.md)
- [Contexto breve](specs/PROJECT_CONTEXT.md)
- [Arquitectura e inyección de dependencias](specs/ARCHITECTURE.md)
- [Componentes compartidos](specs/SHARED_COMPONENTS.md)
- [Plantilla de una feature](specs/FEATURE_TEMPLATE.md)
- [Especificación de features](specs/FEATURES.md)
- [Identidad y tenancy](specs/IDENTITY.md)
- [Organizaciones, sedes y equipo](specs/ORGANIZATIONS.md)
- [Checklists y sincronización](specs/CHECKLISTS.md)
- [Contrato de errores HTTP](specs/API_ERRORS.md)
- [Migraciones](specs/MIGRATIONS.md)
- [Seguridad](specs/SECURITY.md)
- [Sesiones, límites y publicación de V2](specs/SESSIONS.md)
- [Academia BPM](specs/TRAINING.md)
- [Railway](RAILWAY.md)

## Requisitos

- Go 1.27.2.
- Docker con Compose para PostgreSQL local.
- PostgreSQL 18 en Railway para producción.

## Ejecución local

El archivo `.env` local está ignorado por Git y apunta al PostgreSQL de
`compose.yaml`.

```bash
docker compose up -d postgres
go run ./cmd/api
```

Para desarrollo con recarga automática se usa Air:

```bash
make dev
```

El primer inicio instala automáticamente la versión compatible fijada en el
`Makefile` dentro de `.tools/bin`. Air observa únicamente `cmd` e `internal`,
compila en `tmp/air` y carga `.env`. Estos directorios están ignorados por Git y
Docker; Air no forma parte de la imagen ni del despliegue de Railway.

Al iniciar correctamente se muestran mensajes coloreados con la conexión a
PostgreSQL y, cuando todo está listo, un panel visual con el puerto y las URLs
disponibles:

```text
INF BPM API starting port=8080 api_base_path=/api/v1
INF PostgreSQL connected successfully status=ready pool_min=2 pool_max=10
INF HTTP server is ready status=listening address=0.0.0.0:8080 port=8080
INF local API available api=http://localhost:8080/api/v1 health=http://localhost:8080/health readiness=http://localhost:8080/ready

╭────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│  ██████╗ ██████╗ ███╗   ███╗   │  BPM API  ·  SERVIDOR EN EJECUCIÓN                                            │
│  ██╔══██╗██╔══██╗████╗ ████║   │  ● API LISTA     ● POSTGRESQL CONECTADO                                       │
│  ██████╔╝██████╔╝██╔████╔██║   │  Ambiente: desarrollo     Versión: dev                                        │
│  ██╔══██╗██╔═══╝ ██║╚██╔╝██║   │  Dirección: 0.0.0.0:8080     Puerto: 8080                                     │
│  ██████╔╝██║     ██║ ╚═╝ ██║   │  API:        http://localhost:8080/api/v1                                    │
│  ╚═════╝ ╚═╝     ╚═╝     ╚═╝   │  Salud:      http://localhost:8080/health                                    │
│                                  │  Disponible: http://localhost:8080/ready                                     │
├──────────────────────────────────┴─────────────────────────────────────────────────────────────────────────────┤
│  By: Kadin, Evelyn y Piero                                                   ● Listo para recibir solicitudes  │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

El puerto se obtiene de `PORT` en `.env`. Si está ocupado, se puede cambiar, por
ejemplo, a `PORT=8081`. El servidor solo anuncia que está listo después de haber
reservado el puerto correctamente. El panel se muestra solo en `development` y
`test`; Railway conserva logs JSON sin códigos de color.

La primera creación del volumen aplica automáticamente
`migraciones/V1__baseline.sql`.
El esquema no vuelve a ejecutarse sobre volúmenes existentes.

Verificación:

```bash
curl -i http://localhost:8080/health
curl -i http://localhost:8080/ready
```

Documentación interactiva:

```text
http://localhost:8080/docs/
```

Pruebas y análisis:

```bash
go test ./...
go test -race ./...
go vet ./...
```

La prueba de PostgreSQL del módulo checklist se activa opcionalmente con
`TEST_DATABASE_URL`, únicamente si la base se llama `bpm_test`. Usa un esquema
temporal aislado; no debe apuntar a producción.

## Configuración

`.env.example` es el contrato completo. En desarrollo Firebase y R2 pueden estar
deshabilitados mientras se construye el primer módulo. En `staging` y
`production` ambos son obligatorios para impedir un despliegue inseguro.

Variables sensibles:

- `DATABASE_URL`
- `FIREBASE_SERVICE_ACCOUNT_JSON_BASE64`
- `R2_ACCESS_KEY_ID`
- `R2_SECRET_ACCESS_KEY`
- `RESEND_API_KEY`
- `SENTRY_DSN`

No deben confirmarse en Git ni copiarse a la aplicación móvil.

## Base de datos

El esquema inicial se encuentra en `migraciones/V1__baseline.sql` y debe
ejecutarse una sola vez sobre una base vacía. La instancia de Railway ya tiene
su propia línea base; a partir de ella, todos los cambios deben agregarse como
migraciones versionadas hacia adelante y no mediante ediciones destructivas al
esquema desplegado.

El pool usa por defecto entre 2 y 10 conexiones. Cada conexión se configura en
UTC con límites para consultas, bloqueos y transacciones abandonadas.

La lectura de organizaciones, sedes, equipo, cursos, incidencias y catálogo de
controles usa paginación por cursor y búsqueda en servidor. Workspace conserva
los indicadores globales autorizados y expone cursores independientes. Contrato,
compatibilidad y pruebas: [ORGANIZATIONS.md](specs/ORGANIZATIONS.md) y
[CHECKLISTS.md](specs/CHECKLISTS.md). No requiere nuevas migraciones ni variables.
