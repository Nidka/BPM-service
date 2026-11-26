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
14. `specs/MAIL.md`
15. `.env.example`

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
Las entregas requieren V4/V5/V6 (y V13 para turnos, movimientos de stock, versiones de plantilla y fotos de limpieza) antes de activar sus APIs; no implican que esas
migraciones ya estén aplicadas en producción. Turnos, movimientos de stock y fotos de limpieza están en OPERATIONS.md, CHECKLISTS.md y MEDIA.md.
Los reportes siguen pendientes.
Alertas y notificaciones están implementadas localmente; leer ALERTS.md y
NOTIFICATIONS.md. Requieren V7 y un worker separado antes de activarlas.

La Academia BPM (cursos versionados con video privado, modo estudio, intentos
calificados en servidor y constancias) está implementada localmente; leer
`specs/TRAINING.md`. Requiere V8 y la variable `TRAINING_VIDEO_MAX_BYTES`.

La gestión de cuentas (editar/desactivar miembros, transferir administración, invitaciones por
correo, dispositivos propios y tipo de establecimiento) está en `specs/ORGANIZATIONS.md`; requiere V12
e `INVITATION_ACCEPT_URL` opcional.

El módulo de investigación CAP (consentimiento, instrumentos versionados, pretest/postest,
resultados por empresa, exportación anonimizada y necesidades) está implementado localmente;
leer `specs/RESEARCH.md`. Requiere V9 y concesiones de investigador otorgadas por SUPER_ADMIN.

Asignaciones, rutas, insignias, evaluación por módulo, video de YouTube y catálogo offline
están en la misma sección de `specs/TRAINING.md` y requieren además V11.
El progreso por lección (`completedLessonId`, `percent`) y "Lo esencial" (`keyPoints`) requieren además V15
(también `xp_awarded` y un índice de limpieza); aplicar V15 antes de desplegar la API.
Los subtítulos WebVTT por lección (`captionsVtt`, RF-45) requieren V16; aplicarla antes de desplegar la API.
El buscador de normativa (RF-46/47, `internal/features/normative`, rutas `/normative/*`) requiere V17; busca con texto completo
en español (sin IA ni servicios externos) solo en la normativa que carga el SUPER_ADMIN.
Los webhooks salientes para n8n (RF-48, `internal/features/webhooks`) requieren V18 en la API y el worker; ver specs/WEBHOOKS.md.

Reportes (cumplimiento, exportación CSV, KPIs, avance de capacitación, constancias de
cumplimiento y correo semanal del worker) y registro de uso para investigación están
implementados localmente; leer `specs/REPORTS.md`. Requieren V10.

El banco oficial de 100 preguntas BPM (ruta por unidades, práctica con repaso espaciado y evaluación sorteada de 5 ítems
con alternativas barajadas por el servidor, V19) está en la última sección de `specs/TRAINING.md`; el contenido se
instala por organización con `cmd/catalogo-bpm`. Requiere V19 antes de desplegar la API.

El aviso push/bandeja de incidencia asignada (`INCIDENT_ASSIGNED`) requiere V21; el backend es seguro sin ella (omite el aviso con una
advertencia). Ver MIGRATIONS.md.

Correo transaccional propio (verificar correo, nueva contraseña, invitaciones) por Resend con respaldo a Firebase: leer
`specs/MAIL.md` (`internal/features/mail`; rutas `/auth/email/*`; variables `MAIL_FROM`, `MAIL_REPLY_TO`, `APP_PUBLIC_URL`).
