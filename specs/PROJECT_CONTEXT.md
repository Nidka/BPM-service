# Contexto breve del proyecto

## Qué estamos construyendo

Una plataforma BPM para restaurantes, cafetines, catering y servicios afines.
Permitirá administrar organizaciones y sedes, ejecutar controles sanitarios,
trabajar sin conexión, adjuntar evidencias privadas, capacitar personal, atender
incidencias y obtener información de investigación CAP.

El piloto contempla aproximadamente 10–50 usuarios. Por eso se usa un monolito
modular: mantiene bajo el costo operativo sin perder separación interna.

## Tecnología decidida

| Área | Decisión |
|---|---|
| Backend | Go 1.27, Chi y `net/http` |
| Arquitectura | Hexagonal + Clean Architecture + SOLID pragmático |
| Datos | PostgreSQL 18 y `pgxpool` |
| Identidad | Firebase Auth; la API no almacena contraseñas |
| Push | Firebase Cloud Messaging |
| Archivos | Cloudflare R2 privado y URLs firmadas |
| Infraestructura | Railway para API y PostgreSQL |
| Perímetro | Cloudflare DNS/WAF/CDN |
| Correo | Resend mediante outbox |
| Cliente | Kotlin Multiplatform, MVVM, base local y sincronización offline |

## Estado actual

- El esquema PostgreSQL tiene 52 tablas, 2 vistas y auditoría.
- PostgreSQL está creado en Railway.
- La API base compila, abre un pool, expone `/health` y `/ready` y termina de
  forma controlada.
- Firebase verifica sesiones; multimedia privada tiene reserva de carga,
  confirmación y descarga por pregunta de control. Activación R2 y alcance en MEDIA.md.
- Existe middleware de seguridad, logs estructurados, Sentry opcional, Docker,
  Railway y CI.
- Identidad/tenancy ya implementa el primer recorrido autenticado
  `GET /api/v1/me`, que resuelve usuario, organizaciones, roles y locales.
- Organizaciones permite crear tenant y sedes, agregar miembros existentes y
  consultar un espacio de trabajo autorizado.
- Checklists permite publicar una primera versión, descargar catálogo,
  sincronizar respuestas de borrador y enviar con puntaje calculado en servidor.
- Operación diaria implementa catálogos, criterio térmico, recepción de insumos,
  consulta PEPS y planes/ejecuciones de limpieza con permisos por rol/sede.
  Requiere V5 antes de desplegar; límites y pendientes en OPERATIONS.md.

## Flujo de una solicitud

```text
Cliente
  → middleware HTTP (request ID, límites, Firebase)
  → handler (DTO y validación de formato)
  → caso de uso (reglas, permisos y transacción)
  → puerto/interfaz
  → adaptador PostgreSQL, R2, FCM o Resend
```

La respuesta vuelve por el mismo camino. Los errores internos se traducen a un
código estable, mensaje seguro y `requestId`; no se exponen errores SQL.

## Fuentes de verdad

1. `migraciones/V1__baseline.sql`: estructura inicial de datos.
2. `specs/FEATURES.md`: alcance técnico de cada módulo.
3. `specs/ARCHITECTURE.md`: reglas para organizar el código.
4. `specs/SECURITY.md`: controles obligatorios.
5. `.env.example`: contrato de configuración.

Si un requisito contradice el esquema o estas decisiones, se documenta primero
la decisión y después se modifica el código. No se crean implementaciones
paralelas para resolver la contradicción silenciosamente.

## Próximo recorrido

1. Completar asignaciones y nuevas versiones; catálogo e historial de ejecuciones
   ya disponen de páginas y detalle autorizado.
2. Implementar evidencias privadas R2 con firmas temporales.
3. Ampliar operación diaria e incidencias como módulos verticales.

## Qué no hacer

- No dividir en microservicios durante el piloto.
- No usar Firestore como segunda base transaccional.
- No aceptar puntuaciones, roles, tenant o resultados calculados por el cliente.
- No subir archivos a través del proceso API ni guardar URLs firmadas.
- No introducir repositorios genéricos; cada módulo expresa consultas concretas.
- No compartir modelos de base de datos directamente como respuestas HTTP.

