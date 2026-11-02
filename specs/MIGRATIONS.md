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
