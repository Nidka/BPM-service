BEGIN;

-- Academia BPM: cambios incrementales sobre las tablas de capacitación de V1.
-- Preflight: la migración se detiene si ya hay datos que violarían las nuevas
-- unicidades. Nunca borra ni fusiona registros automáticamente.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM training_course_versions WHERE status = 'DRAFT' GROUP BY organization_id, course_id HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'V8 preflight: hay cursos con más de una versión DRAFT';
 END IF;
 IF EXISTS (SELECT 1 FROM training_course_versions WHERE status = 'PUBLISHED' GROUP BY organization_id, course_id HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'V8 preflight: hay cursos con más de una versión PUBLISHED';
 END IF;
 IF EXISTS (SELECT 1 FROM training_certificates GROUP BY organization_id, user_id, course_version_id HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'V8 preflight: hay constancias duplicadas por organización, usuario y versión';
 END IF;
END
$$;

-- Control optimista del borrador: cada guardado incrementa la versión de fila.
ALTER TABLE training_course_versions
 ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0);

-- Huella canónica de la solicitud de un intento para distinguir reintentos
-- idénticos (misma respuesta) de reutilización del mismo identificador con otro contenido.
ALTER TABLE training_attempts
 ADD COLUMN request_hash VARCHAR(64) CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$');

-- Una sola versión en preparación y una sola vigente por curso.
CREATE UNIQUE INDEX training_versions_one_draft_uq
 ON training_course_versions (organization_id, course_id) WHERE status = 'DRAFT';
CREATE UNIQUE INDEX training_versions_one_published_uq
 ON training_course_versions (organization_id, course_id) WHERE status = 'PUBLISHED';

-- Constancia única por organización, usuario y versión aprobada.
CREATE UNIQUE INDEX training_certificates_user_version_uq
 ON training_certificates (organization_id, user_id, course_version_id);
CREATE INDEX training_certificates_user_issued_idx
 ON training_certificates (organization_id, user_id, issued_at DESC);

-- Un mismo intento solo puede tener una respuesta por pregunta (ya existe) y los
-- resultados se leen por versión y usuario.
CREATE INDEX training_attempts_version_user_idx
 ON training_attempts (organization_id, course_version_id, user_id);
CREATE INDEX training_questions_lesson_idx
 ON training_questions (organization_id, lesson_id) WHERE lesson_id IS NOT NULL;
CREATE INDEX training_lessons_media_idx
 ON training_lessons (organization_id, media_file_id) WHERE media_file_id IS NOT NULL;

-- Videos de capacitación: archivos sin sede dentro de organizations/{org}/training/videos/.
CREATE INDEX media_files_training_video_idx
 ON media_files (organization_id, created_at DESC) WHERE location_id IS NULL AND mime_type LIKE 'video/%';

-- Límites de contenido. NOT VALID protege las filas nuevas sin reescribir datos previos.
ALTER TABLE training_questions
 ADD CONSTRAINT training_questions_options_count_check
 CHECK (jsonb_typeof(options) = 'array' AND jsonb_array_length(options) BETWEEN 2 AND 6) NOT VALID;
ALTER TABLE training_questions
 ADD CONSTRAINT training_questions_correct_in_range_check
 CHECK (jsonb_typeof(options) = 'array' AND correct_option < jsonb_array_length(options)) NOT VALID;
ALTER TABLE training_lessons
 ADD CONSTRAINT training_lessons_content_length_check
 CHECK (char_length(content) <= 20000) NOT VALID;

COMMIT;
