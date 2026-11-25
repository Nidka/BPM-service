-- Banco oficial de preguntas BPM (100 ítems, versionado): metadatos por pregunta, registro del ítem y su
-- versión en cada respuesta de un intento, sorteo persistido de cada evaluación (opciones aleatorizadas
-- en el servidor) y resultados de práctica para el repaso espaciado. Aditiva sobre V1, V8 y V11: solo
-- agrega columnas opcionales y tablas nuevas; no reescribe ni borra datos y la app anterior sigue funcionando.
BEGIN;

-- Metadatos del ítem. item_code NULL = pregunta común de autoría (sin banco).
ALTER TABLE training_questions
 ADD COLUMN IF NOT EXISTS item_code VARCHAR(20),
 ADD COLUMN IF NOT EXISTS bank_version INTEGER,
 ADD COLUMN IF NOT EXISTS bloom_level VARCHAR(20),
 ADD COLUMN IF NOT EXISTS source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
 ADD CONSTRAINT training_questions_bank_check CHECK (
  (item_code IS NULL AND bank_version IS NULL AND bloom_level IS NULL)
  OR (item_code IS NOT NULL AND bank_version >= 1 AND bloom_level IN ('Recordar','Comprender','Aplicar','Analizar','Evaluar'))),
 ADD CONSTRAINT training_questions_source_refs_check CHECK (jsonb_typeof(source_refs) = 'array');
CREATE UNIQUE INDEX IF NOT EXISTS training_questions_item_code_uq
 ON training_questions (course_version_id, item_code) WHERE item_code IS NOT NULL;

-- Respuesta registrada con el ítem, su versión y el orden en que se mostraron las alternativas
-- (posición mostrada -> índice original). selected_option y is_correct usan el índice original.
ALTER TABLE training_attempt_answers
 ADD COLUMN IF NOT EXISTS item_code VARCHAR(20),
 ADD COLUMN IF NOT EXISTS bank_version INTEGER,
 ADD COLUMN IF NOT EXISTS option_order JSONB,
 ADD CONSTRAINT training_attempt_answers_option_order_check CHECK (option_order IS NULL OR jsonb_typeof(option_order) = 'array');

-- Sorteo de una evaluación: qué ítems se mostraron y con qué orden de alternativas. Su id es el id del intento.
CREATE TABLE IF NOT EXISTS training_evaluation_draws (
    id UUID PRIMARY KEY,
    organization_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    course_version_id UUID NOT NULL,
    attempt_type VARCHAR(20) NOT NULL,
    bank_version INTEGER NOT NULL,
    attempt_number INTEGER NOT NULL,
    items JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_memberships(organization_id, user_id),
    FOREIGN KEY (organization_id, course_version_id) REFERENCES training_course_versions(organization_id, id),
    CONSTRAINT training_evaluation_draws_type_check CHECK (attempt_type IN ('DIAGNOSTIC', 'FINAL')),
    CONSTRAINT training_evaluation_draws_items_check CHECK (jsonb_typeof(items) = 'array' AND jsonb_array_length(items) BETWEEN 1 AND 20)
);
CREATE INDEX IF NOT EXISTS training_evaluation_draws_user_idx ON training_evaluation_draws (organization_id, user_id, course_version_id, created_at);
CREATE TRIGGER training_evaluation_draws_set_updated_at BEFORE UPDATE ON training_evaluation_draws FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Práctica por ítem (no es evaluación): alimenta el repaso espaciado, el XP de práctica y la meta del día.
-- box 0 = fallado (toca repasar ya); 1..3 = acertado, vuelve a tocar en 1, 3 y 7 días; 4 = dominado.
CREATE TABLE IF NOT EXISTS training_practice_results (
    organization_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    course_id UUID NOT NULL,
    item_code VARCHAR(20) NOT NULL,
    answered_count INTEGER NOT NULL DEFAULT 0,
    correct_count INTEGER NOT NULL DEFAULT 0,
    last_correct BOOLEAN NOT NULL,
    box SMALLINT NOT NULL DEFAULT 0,
    due_at TIMESTAMPTZ,
    experience_points INTEGER NOT NULL DEFAULT 0,
    last_answered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    PRIMARY KEY (organization_id, user_id, course_id, item_code),
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_memberships(organization_id, user_id),
    FOREIGN KEY (organization_id, course_id) REFERENCES training_courses(organization_id, id),
    CONSTRAINT training_practice_results_box_check CHECK (box BETWEEN 0 AND 4),
    CONSTRAINT training_practice_results_counts_check CHECK (answered_count >= correct_count AND correct_count >= 0 AND experience_points >= 0)
);
CREATE INDEX IF NOT EXISTS training_practice_results_due_idx ON training_practice_results (organization_id, user_id, course_id, due_at);
CREATE INDEX IF NOT EXISTS training_practice_results_day_idx ON training_practice_results (organization_id, user_id, last_answered_at);
CREATE TRIGGER training_practice_results_set_updated_at BEFORE UPDATE ON training_practice_results FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
