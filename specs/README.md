# Especificaciones del backend

Esta carpeta es la fuente de verdad técnica para continuar el backend.

Orden recomendado de lectura:

1. `PROJECT_CONTEXT.md`: propósito, tecnología y estado actual.
2. `ARCHITECTURE.md`: estructura, dependencias e inyección.
3. `DEVELOPMENT.md`: herramientas y flujo de desarrollo local.
4. `SHARED_COMPONENTS.md`: política para código compartido y utilidades.
5. `FEATURE_TEMPLATE.md`: forma exacta de implementar una feature.
6. `FEATURES.md`: alcance y orden de los módulos funcionales.
7. `IDENTITY.md`: contrato del primer feature y `/api/v1/me`.
8. `ORGANIZATIONS.md`: tenant activo, sedes, equipo, permisos y límites.
9. `CHECKLISTS.md`: primer recorrido operativo y sincronización offline.
10. `API_ERRORS.md`: formato uniforme y sanitizado de errores HTTP.
11. `OPENAPI.md`: contrato HTTP y Swagger UI embebida.
12. `MIGRATIONS.md`: convención y flujo de cambios PostgreSQL.
13. `SECURITY.md`: controles obligatorios.
14. `SESSIONS.md`: límites de sesión, migración V2 y publicación coordinada.

Las especificaciones describen decisiones vigentes. Si una decisión cambia, se
actualiza aquí en el mismo cambio que modifica el código.
