BEGIN;

-- Módulo de investigación CAP (conocimientos, actitudes y prácticas): cambios
-- incrementales sobre las tablas de investigación de V1.
-- Preflight: la migración se detiene si ya existen datos que violarían las nuevas
-- unicidades. Nunca borra ni fusiona registros automáticamente.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM research_participants WHERE user_id IS NOT NULL GROUP BY organization_id, user_id HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'V9 preflight: hay usuarios con más de un participante en la misma organización';
 END IF;
 IF EXISTS (SELECT 1 FROM research_participants GROUP BY participant_code HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'V9 preflight: hay códigos de participante repetidos entre organizaciones';
 END IF;
 IF EXISTS (SELECT 1 FROM survey_instrument_versions WHERE status IN ('DRAFT', 'PUBLISHED') GROUP BY instrument_id, status HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'V9 preflight: hay instrumentos con más de una versión DRAFT o PUBLISHED';
 END IF;
END
$$;

-- Instrumento oficial CAP: solo puede existir uno y su bandera no cambia.
ALTER TABLE survey_instruments ADD COLUMN is_cap BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX survey_instruments_one_cap_uq ON survey_instruments ((is_cap)) WHERE is_cap;

-- Control optimista del borrador y una sola versión en preparación y vigente.
ALTER TABLE survey_instrument_versions
 ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0);
CREATE UNIQUE INDEX survey_versions_one_draft_uq ON survey_instrument_versions (instrument_id) WHERE status = 'DRAFT';
CREATE UNIQUE INDEX survey_versions_one_published_uq ON survey_instrument_versions (instrument_id) WHERE status = 'PUBLISHED';

-- Respuesta correcta (solo opción múltiple) y dimensión CAP de cada pregunta.
ALTER TABLE survey_questions
 ADD COLUMN correct_option INTEGER CHECK (correct_option IS NULL OR correct_option >= 0);
ALTER TABLE survey_questions
 ADD CONSTRAINT survey_questions_section_check
 CHECK (section IN ('KNOWLEDGE', 'ATTITUDE', 'PRACTICE')) NOT VALID;
ALTER TABLE survey_questions
 ADD CONSTRAINT survey_questions_section_type_check
 CHECK ((answer_type = 'MULTIPLE_CHOICE' AND section = 'KNOWLEDGE')
     OR (answer_type = 'LIKERT' AND section = 'ATTITUDE')
     OR (answer_type = 'FREQUENCY' AND section = 'PRACTICE')) NOT VALID;
ALTER TABLE survey_questions
 ADD CONSTRAINT survey_questions_correct_type_check
 CHECK (correct_option IS NULL OR (answer_type = 'MULTIPLE_CHOICE' AND options IS NOT NULL AND correct_option < jsonb_array_length(options))) NOT VALID;

-- Entrega: huella de la solicitud (idempotencia) y puntajes calculados por el servidor.
ALTER TABLE survey_submissions
 ADD COLUMN request_hash VARCHAR(64) CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$'),
 ADD COLUMN knowledge_correct INTEGER CHECK (knowledge_correct IS NULL OR knowledge_correct >= 0),
 ADD COLUMN knowledge_total INTEGER CHECK (knowledge_total IS NULL OR knowledge_total >= 0),
 ADD COLUMN knowledge_score NUMERIC(5, 2) CHECK (knowledge_score IS NULL OR knowledge_score BETWEEN 0 AND 100),
 ADD COLUMN attitude_score NUMERIC(4, 2) CHECK (attitude_score IS NULL OR attitude_score BETWEEN 1 AND 5),
 ADD COLUMN practice_score NUMERIC(4, 2) CHECK (practice_score IS NULL OR practice_score BETWEEN 1 AND 5);
ALTER TABLE survey_answers ADD COLUMN is_correct BOOLEAN;
CREATE INDEX survey_submissions_version_idx ON survey_submissions (instrument_version_id, organization_id, moment);

-- Un usuario es un único participante por organización y el código seudónimo es único global.
CREATE UNIQUE INDEX research_participants_user_uq ON research_participants (organization_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX research_participants_code_uq ON research_participants (participant_code);
CREATE INDEX consent_records_latest_idx ON consent_records (organization_id, participant_id, accepted_at DESC, created_at DESC);
ALTER TABLE consent_records ADD COLUMN responsible_investigator VARCHAR(200) NOT NULL DEFAULT '';

-- Documentos de consentimiento informado versionados; solo uno está ACTIVE.
CREATE TABLE research_consent_documents (
    version VARCHAR(40) PRIMARY KEY,
    title VARCHAR(200) NOT NULL,
    body_text TEXT NOT NULL,
    responsible_investigator VARCHAR(200) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    CONSTRAINT research_consent_documents_status_check CHECK (status IN ('ACTIVE', 'ARCHIVED')),
    CONSTRAINT research_consent_documents_text_check CHECK (char_length(body_text) <= 20000)
);
CREATE UNIQUE INDEX research_consent_documents_one_active_uq ON research_consent_documents ((status)) WHERE status = 'ACTIVE';

-- Concesión de acceso de investigación, separada de los roles de empresa.
-- La gestiona exclusivamente SUPER_ADMIN; revocar conserva el historial.
CREATE TABLE research_access_grants (
    user_id VARCHAR(128) PRIMARY KEY REFERENCES application_users(id),
    granted_by VARCHAR(128) NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    revoked_by VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128)
);

-- RF-50: necesidades detectadas por investigadores en las empresas participantes.
CREATE TABLE research_company_needs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id VARCHAR(64) NOT NULL REFERENCES organizations(id),
    category VARCHAR(30) NOT NULL,
    description TEXT NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL,
    investigator_user_id VARCHAR(128) NOT NULL REFERENCES application_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    CONSTRAINT research_company_needs_category_check
        CHECK (category IN ('TRAINING', 'PROCESS', 'INFRASTRUCTURE', 'DOCUMENTATION', 'TECHNOLOGY', 'SUPPLIERS', 'OTHER')),
    CONSTRAINT research_company_needs_description_check CHECK (char_length(description) BETWEEN 1 AND 2000)
);
CREATE INDEX research_company_needs_list_idx ON research_company_needs (organization_id, detected_at DESC, id DESC);
CREATE INDEX research_company_needs_all_idx ON research_company_needs (detected_at DESC, id DESC);

CREATE TRIGGER research_consent_documents_set_updated_at BEFORE UPDATE ON research_consent_documents FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER research_access_grants_set_updated_at BEFORE UPDATE ON research_access_grants FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER research_company_needs_set_updated_at BEFORE UPDATE ON research_company_needs FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Inmutabilidad: una versión publicada o archivada y sus preguntas no se modifican;
-- las respuestas, entregas y consentimientos ya registrados tampoco.
CREATE FUNCTION research_version_is_open(version_id UUID) RETURNS BOOLEAN AS $$
 SELECT COALESCE((SELECT status = 'DRAFT' FROM survey_instrument_versions WHERE id = version_id), TRUE)
$$ LANGUAGE sql STABLE;

CREATE FUNCTION research_questions_guard() RETURNS trigger AS $$
BEGIN
 IF TG_OP IN ('UPDATE', 'DELETE') AND NOT research_version_is_open(OLD.instrument_version_id) THEN
  RAISE EXCEPTION 'la versión % del instrumento es inmutable', OLD.instrument_version_id USING ERRCODE = '23514';
 END IF;
 IF TG_OP IN ('INSERT', 'UPDATE') AND NOT research_version_is_open(NEW.instrument_version_id) THEN
  RAISE EXCEPTION 'la versión % del instrumento es inmutable', NEW.instrument_version_id USING ERRCODE = '23514';
 END IF;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END
$$ LANGUAGE plpgsql;
CREATE TRIGGER survey_questions_immutable BEFORE INSERT OR UPDATE OR DELETE ON survey_questions
 FOR EACH ROW EXECUTE FUNCTION research_questions_guard();

CREATE FUNCTION research_versions_guard() RETURNS trigger AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  IF OLD.status <> 'DRAFT' THEN
   RAISE EXCEPTION 'la versión % del instrumento es inmutable', OLD.id USING ERRCODE = '23514';
  END IF;
  RETURN OLD;
 END IF;
 IF OLD.status <> 'DRAFT' THEN
  IF NEW.status NOT IN ('PUBLISHED', 'ARCHIVED') OR (OLD.status = 'ARCHIVED' AND NEW.status <> 'ARCHIVED')
     OR NEW.instrument_id <> OLD.instrument_id OR NEW.version_number <> OLD.version_number
     OR NEW.published_at IS DISTINCT FROM OLD.published_at THEN
   RAISE EXCEPTION 'la versión % del instrumento es inmutable', OLD.id USING ERRCODE = '23514';
  END IF;
 END IF;
 RETURN NEW;
END
$$ LANGUAGE plpgsql;
CREATE TRIGGER survey_versions_immutable BEFORE UPDATE OR DELETE ON survey_instrument_versions
 FOR EACH ROW EXECUTE FUNCTION research_versions_guard();

CREATE FUNCTION research_records_immutable() RETURNS trigger AS $$
BEGIN
 RAISE EXCEPTION 'los registros de % son inmutables', TG_TABLE_NAME USING ERRCODE = '23514';
END
$$ LANGUAGE plpgsql;
CREATE TRIGGER survey_answers_immutable BEFORE UPDATE OR DELETE ON survey_answers
 FOR EACH ROW EXECUTE FUNCTION research_records_immutable();
CREATE TRIGGER survey_submissions_immutable BEFORE UPDATE OR DELETE ON survey_submissions
 FOR EACH ROW EXECUTE FUNCTION research_records_immutable();
CREATE TRIGGER consent_records_immutable BEFORE UPDATE OR DELETE ON consent_records
 FOR EACH ROW EXECUTE FUNCTION research_records_immutable();

COMMIT;
