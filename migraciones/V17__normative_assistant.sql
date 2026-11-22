-- RF-46/47: agente normativo. El administrador general carga la normativa (texto) y cualquier persona
-- activa consulta; las respuestas citan solo esos documentos. Las preguntas se registran (auditoría y
-- límite por hora). No contiene datos de empresas.
BEGIN;

CREATE TABLE IF NOT EXISTS normative_documents (
    id UUID PRIMARY KEY,
    title VARCHAR(200) NOT NULL CHECK (char_length(btrim(title)) >= 1),
    source VARCHAR(300) NOT NULL DEFAULT '',
    content TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 400000),
    status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'ARCHIVED')),
    created_by VARCHAR(128) NOT NULL REFERENCES application_users(id),
    updated_by VARCHAR(128) NOT NULL REFERENCES application_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS normative_documents_status_idx ON normative_documents (status, updated_at DESC);

CREATE TABLE IF NOT EXISTS normative_questions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(128) NOT NULL REFERENCES application_users(id),
    question VARCHAR(1000) NOT NULL CHECK (char_length(btrim(question)) >= 1),
    answer TEXT NOT NULL DEFAULT '',
    citations JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(citations) = 'array'),
    declined BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS normative_questions_user_idx ON normative_questions (user_id, created_at DESC);

COMMIT;
