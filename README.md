# Backend BPM

Base del backend BPM implementada en Go como monolito modular con arquitectura
hexagonal, Clean Architecture y dependencias orientadas por interfaces.

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
