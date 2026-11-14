# Migraciones PostgreSQL

## Ubicación y formato

Todas las migraciones viven en `migraciones/`, dentro del backend:

```text
migraciones/
  V1__baseline.sql
  V2__descripcion_del_cambio.sql
  V3__otra_descripcion.sql
```

Se usa el formato `V<numero>__<descripcion>.sql`, con número creciente y sin
reutilizar versiones. `V1__baseline.sql` representa la estructura inicial ya
instalada.

## Reglas

1. Una migración desplegada es inmutable.
2. Todo cambio posterior se agrega en un archivo nuevo y avanza hacia adelante.
3. Cada archivo debe ser transaccional cuando PostgreSQL lo permita.
4. Los cambios de datos deben ser idempotentes o estar protegidos por la versión.
5. Índices grandes y cambios incompatibles requieren estrategia de despliegue.
6. Nunca se incluyen credenciales, datos reales ni URLs de conexión.
7. El pipeline debe ejecutar primero migraciones y después iniciar la API.

## Runner previsto

El runner se implementará como un comando separado, `cmd/migrate`, no dentro
del arranque HTTP. Usará `DATABASE_URL`, un bloqueo asesor de PostgreSQL y una
tabla de control `schema_migrations`. Así se evita que varias réplicas intenten
migrar simultáneamente y se mantiene una sola responsabilidad por proceso.

Para una base existente que ya contiene el baseline, `V1` se registra como
aplicada sin volver a ejecutar sus `CREATE TABLE`. En instalaciones nuevas se
ejecuta desde cero.

Hasta implementar el runner, la ejecución es manual y debe usar
`ON_ERROR_STOP=1`; Railway no debe ejecutar automáticamente el baseline sobre la
base ya creada.

`V2__authentication_sessions.sql` añade exclusivamente el estado técnico de
sesiones. Debe aplicarse antes de desplegar el backend que la exige; consultar
[SESSIONS.md](SESSIONS.md). Los volúmenes Docker ya existentes no ejecutan scripts
de inicialización nuevos automáticamente: aplicar V2 una vez en la base local
correspondiente. No borrar volúmenes con datos para forzar la inicialización.

## Evidencias de controles (V4)

`V4__checklist_evidence_items.sql` sustituye el vínculo de evidencia a respuesta
por pregunta, preservando las asociaciones existentes. Aplicar una sola vez
después de V2, antes del backend que la exige; consultar [MEDIA.md](MEDIA.md).
V3 local de capacitación permanece fuera de esta entrega. No ejecutar el
baseline de nuevo ni incluir borradores de migraciones en el despliegue.

## Operación diaria (V5)

`V5__daily_operations.sql` agrega criterio térmico, instantáneas de recepción,
hashes e índices de operación diaria. Aplicar después de V4 y antes del backend
que exige este esquema. Comprobar antes los lotes duplicados por ítem siguiendo
[OPERATIONS.md](OPERATIONS.md); no se eliminan datos antiguos automáticamente.
V3 local de capacitación sigue fuera de esta entrega.

## Incidencias (V6)

`V6__incidents.sql` añade catálogo versionado e historial inmutable, versión
optimista y recibos privados de comandos. Aplicar después de V5, antes de la API
que exige este esquema. Revisar el preflight de [INCIDENTS.md](INCIDENTS.md); no
borrar ni fusionar acciones o asociaciones históricas automáticamente. V3 local
de capacitación continúa fuera del despliegue.

## Alertas y notificaciones (V7)

`V7__alerts_notifications.sql` añade versiones, snapshots, deduplicación, leases
y destinos privados. Aplicar después de V6 y antes de la API/worker de esta
entrega. El preflight rechaza entregas legadas duplicadas por organización,
aviso y canal; revisar su historial manualmente, sin borrar ni fusionar registros.
La migración no crea reglas, cuentas, contenidos ni asignaciones de prueba.
V3 local sigue fuera de esta entrega. Ver [ALERTS.md](ALERTS.md) y
[NOTIFICATIONS.md](NOTIFICATIONS.md); el worker se despliega aparte.
