BEGIN;

-- Academia BPM, pendientes del rol Personal: progreso por lección, "Lo esencial" por lección,
-- XP persistido por intento e índice de limpieza por plan. Aditiva sobre V1, V5, V8 y V11:
-- no borra ni reescribe datos y la app anterior sigue funcionando sin cambios.

-- Lecciones completadas por persona. source distingue "vista" (el cliente avanzó) de
-- "módulo aprobado" (el servidor calificó un intento MODULE). La clave primaria hace idempotente
-- repetir la marca. Las lecciones de una versión nueva tienen IDs nuevos y arrancan sin marcas.
CREATE TABLE IF NOT EXISTS training_lesson_progress (
    organization_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    course_id UUID NOT NULL,
    lesson_id UUID NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source VARCHAR(20) NOT NULL DEFAULT 'VIEWED',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    PRIMARY KEY (organization_id, user_id, lesson_id),
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_memberships(organization_id, user_id),
    FOREIGN KEY (organization_id, course_id) REFERENCES training_courses(organization_id, id),
    FOREIGN KEY (organization_id, lesson_id) REFERENCES training_lessons(organization_id, id),
    CONSTRAINT training_lesson_progress_source_check CHECK (source IN ('VIEWED', 'MODULE_PASSED'))
);
CREATE INDEX IF NOT EXISTS training_lesson_progress_user_course_idx ON training_lesson_progress (organization_id, user_id, course_id);
CREATE INDEX IF NOT EXISTS training_lesson_progress_user_day_idx ON training_lesson_progress (organization_id, user_id, completed_at);
CREATE TRIGGER training_lesson_progress_set_updated_at BEFORE UPDATE ON training_lesson_progress FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- "Lo esencial": hasta 5 puntos clave por lección (la longitud de cada ítem se valida en el dominio).
ALTER TABLE training_lessons
 ADD COLUMN IF NOT EXISTS key_points JSONB NOT NULL DEFAULT '[]'::jsonb,
 ADD CONSTRAINT training_lessons_key_points_check CHECK (jsonb_typeof(key_points) = 'array' AND jsonb_array_length(key_points) <= 5);

-- XP concedido por el intento (curso + módulo); NULL en intentos anteriores a esta migración.
ALTER TABLE training_attempts
 ADD COLUMN IF NOT EXISTS xp_awarded INTEGER,
 ADD CONSTRAINT training_attempts_xp_awarded_check CHECK (xp_awarded IS NULL OR xp_awarded >= 0);

-- Última ejecución de limpieza por plan (tareas de hoy).
CREATE INDEX IF NOT EXISTS cleaning_executions_plan_idx ON cleaning_executions (organization_id, cleaning_plan_id, executed_at DESC);

COMMIT;
