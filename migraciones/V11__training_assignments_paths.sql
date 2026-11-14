BEGIN;

-- Academia BPM, segunda entrega: asignaciones, rutas, insignias, evaluación por
-- módulo y video externo de YouTube. Aditiva sobre V1 y V8; no borra datos.

-- Video externo (solo YouTube) por lección: nunca se guarda una URL, solo el id de 11 caracteres.
ALTER TABLE training_lessons
 ADD COLUMN external_video_provider VARCHAR(20),
 ADD COLUMN external_video_id VARCHAR(11),
 ADD CONSTRAINT training_lessons_external_video_check CHECK (
  (external_video_provider IS NULL AND external_video_id IS NULL)
  OR (external_video_provider = 'YOUTUBE' AND external_video_id ~ '^[A-Za-z0-9_-]{11}$')),
 ADD CONSTRAINT training_lessons_single_video_check CHECK (external_video_id IS NULL OR media_file_id IS NULL);

-- Asignaciones: una persona, un rol (opcionalmente en una sede) o una sede completa.
ALTER TABLE training_assignments DROP CONSTRAINT training_assignments_target_check;
ALTER TABLE training_assignments
 ADD CONSTRAINT training_assignments_scope_check
  CHECK (target_user_id IS NOT NULL OR target_role IS NOT NULL OR location_id IS NOT NULL),
 ADD COLUMN cancelled_at TIMESTAMPTZ,
 ADD COLUMN cancelled_by VARCHAR(128),
 ADD CONSTRAINT training_assignments_cancel_check CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)) NOT VALID;
-- Una misma asignación activa no puede repetirse (mismo curso y mismo destinatario).
CREATE UNIQUE INDEX training_assignments_active_uq
 ON training_assignments (organization_id, course_id, COALESCE(target_user_id, ''), COALESCE(target_role, ''), COALESCE(location_id::text, ''))
 WHERE status = 'ACTIVE';
CREATE INDEX training_assignments_list_idx ON training_assignments (organization_id, status, created_at DESC, id);
CREATE INDEX training_assignments_course_idx ON training_assignments (organization_id, course_id);
CREATE INDEX training_assignments_location_idx ON training_assignments (organization_id, location_id) WHERE location_id IS NOT NULL;

-- Evaluación por módulo: el intento MODULE referencia la lección evaluada.
ALTER TABLE training_attempts
 ADD COLUMN lesson_id UUID,
 ADD CONSTRAINT training_attempts_lesson_fk FOREIGN KEY (organization_id, lesson_id) REFERENCES training_lessons(organization_id, id),
 ADD CONSTRAINT training_attempts_module_lesson_check CHECK ((attempt_type = 'MODULE') = (lesson_id IS NOT NULL)) NOT VALID;
CREATE INDEX training_attempts_user_day_idx ON training_attempts (organization_id, user_id, completed_at);

-- Mejor resultado por módulo para que repetir intentos no multiplique el XP.
CREATE TABLE training_module_results (
    organization_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    course_id UUID NOT NULL,
    lesson_id UUID NOT NULL,
    best_score INTEGER NOT NULL,
    passed BOOLEAN NOT NULL DEFAULT FALSE,
    experience_points INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    PRIMARY KEY (organization_id, user_id, lesson_id),
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_memberships(organization_id, user_id),
    FOREIGN KEY (organization_id, course_id) REFERENCES training_courses(organization_id, id),
    FOREIGN KEY (organization_id, lesson_id) REFERENCES training_lessons(organization_id, id),
    CONSTRAINT training_module_results_score_check CHECK (best_score BETWEEN 0 AND 100),
    CONSTRAINT training_module_results_points_check CHECK (experience_points >= 0)
);
CREATE INDEX training_module_results_user_idx ON training_module_results (organization_id, user_id);
CREATE TRIGGER training_module_results_set_updated_at BEFORE UPDATE ON training_module_results FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Insignias: el criterio lo evalúa solo el servidor; el catálogo por defecto se
-- materializa por organización (INSERT ... ON CONFLICT DO NOTHING) desde el código.
ALTER TABLE training_achievements
 ADD COLUMN criterion_type VARCHAR(30),
 ADD COLUMN criterion_threshold INTEGER NOT NULL DEFAULT 1,
 ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE,
 ADD CONSTRAINT training_achievements_criterion_check CHECK (criterion_type IS NULL OR criterion_type IN ('COURSES_COMPLETED', 'PERFECT_SCORE', 'STREAK_DAYS')),
 ADD CONSTRAINT training_achievements_threshold_check CHECK (criterion_threshold BETWEEN 1 AND 100);
ALTER TABLE user_achievements
 ADD COLUMN source_attempt_id UUID,
 ADD CONSTRAINT user_achievements_attempt_fk FOREIGN KEY (organization_id, source_attempt_id) REFERENCES training_attempts(organization_id, id);
CREATE INDEX user_achievements_attempt_idx ON user_achievements (organization_id, source_attempt_id) WHERE source_attempt_id IS NOT NULL;

-- Rutas de aprendizaje: secuencia ordenada de cursos por organización.
CREATE TABLE learning_paths (
    id UUID PRIMARY KEY,
    organization_id VARCHAR(64) NOT NULL REFERENCES organizations(id),
    title VARCHAR(200) NOT NULL,
    description TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'DRAFT',
    row_version INTEGER NOT NULL DEFAULT 1,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    UNIQUE (organization_id, id),
    CONSTRAINT learning_paths_status_check CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    CONSTRAINT learning_paths_row_version_check CHECK (row_version > 0)
);
CREATE TABLE learning_path_courses (
    organization_id VARCHAR(64) NOT NULL,
    path_id UUID NOT NULL,
    course_id UUID NOT NULL,
    position INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    PRIMARY KEY (path_id, course_id),
    UNIQUE (path_id, position),
    FOREIGN KEY (organization_id, path_id) REFERENCES learning_paths(organization_id, id),
    FOREIGN KEY (organization_id, course_id) REFERENCES training_courses(organization_id, id),
    CONSTRAINT learning_path_courses_position_check CHECK (position > 0)
);
CREATE INDEX learning_paths_list_idx ON learning_paths (organization_id, status, title, id);
CREATE INDEX learning_path_courses_course_idx ON learning_path_courses (organization_id, course_id);
CREATE TRIGGER learning_paths_set_updated_at BEFORE UPDATE ON learning_paths FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER learning_path_courses_set_updated_at BEFORE UPDATE ON learning_path_courses FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
