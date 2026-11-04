# Guía para continuar el backend BPM

Antes de modificar código, leer en este orden:

1. `specs/PROJECT_CONTEXT.md`
2. `specs/ARCHITECTURE.md`
3. `specs/DEVELOPMENT.md`
4. `specs/SHARED_COMPONENTS.md`
5. `specs/FEATURE_TEMPLATE.md`
6. `specs/FEATURES.md`
7. `specs/IDENTITY.md`
8. `specs/ORGANIZATIONS.md`
9. `specs/CHECKLISTS.md`
10. `specs/API_ERRORS.md`
11. `specs/OPENAPI.md`
12. `specs/MIGRATIONS.md`
13. `specs/SECURITY.md`
14. `.env.example`

Reglas obligatorias:

- Escribir comentarios, KDoc, GoDoc y documentación nueva en español; conservar los identificadores técnicos y nombres de API.

- Go 1.27, monolito modular, arquitectura hexagonal y composición manual.
- No usar un framework de inyección de dependencias. Los constructores reciben
  interfaces pequeñas y `internal/core/app` conecta las implementaciones.
- `internal/core` contiene únicamente base técnica compartida; todo concepto de
  negocio se implementa como recorrido vertical en `internal/features`.
- `internal/shared/utils` admite únicamente funciones puras con reutilización
  comprobada; nunca reglas de negocio, configuración o infraestructura.
- `domain` no importa HTTP, pgx, Firebase, R2 ni paquetes de otro adaptador.
- Un handler no contiene reglas de negocio ni abre transacciones.
- Toda consulta empresarial filtra `organization_id` obtenido de una membresía
  autenticada; nunca se confía en el tenant enviado por el cliente.
- Usar UUID/idempotencia y control de versión en operaciones offline.
- Archivos privados: solo metadatos en PostgreSQL y contenido en R2.
- No registrar tokens, contraseñas, URLs firmadas, cuerpos ni cadenas de conexión.
- `migraciones/V1__baseline.sql` es la línea base ya desplegada. Los cambios
  posteriores deben ser archivos nuevos e incrementales.
- Antes de entregar: `gofmt`, `go vet ./...`, `go test ./...`,
  `go test -race ./...` y `git diff --check`.

Estado actual: identidad y organizaciones ya permiten perfiles, tenant activo,
altas de organización/sede y adición de miembros; leer `specs/ORGANIZATIONS.md`.
`features/checklists` implementa catálogo, borrador, respuestas, envío y
programación por sede/frecuencia; leer `specs/CHECKLISTS.md`.
`features/media` implementa fotos privadas para controles e incidencias; leer
`specs/MEDIA.md`. Operación diaria estructurada está en `specs/OPERATIONS.md` y
el recorrido de incidencias/correcciones en `specs/INCIDENTS.md`.
Las entregas requieren V4/V5/V6 antes de activar sus APIs; no implican que esas
migraciones ya estén aplicadas en producción. Turnos, movimientos de stock,
fotos de limpieza, capacitación y reportes siguen pendientes.
Alertas y notificaciones están implementadas localmente; leer ALERTS.md y
NOTIFICATIONS.md. Requieren V7 y un worker separado antes de activarlas.

