-- RF-46/47: buscador de normativa. El administrador general carga la normativa (texto) y cualquier persona
-- activa la consulta. Cada norma se divide en fragmentos (párrafos) indexados con búsqueda de texto completo en
-- español, sin distinguir tildes. No usa servicios externos ni contiene datos de empresas.
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

-- Fragmentos buscables; se reemplazan completos cada vez que se edita la norma.
CREATE TABLE IF NOT EXISTS normative_fragments (
    document_id UUID NOT NULL REFERENCES normative_documents(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
    search_vector TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('spanish'::regconfig, translate(lower(body), 'áéíóúüñ', 'aeiouun'))
    ) STORED,
    PRIMARY KEY (document_id, position)
);
CREATE INDEX IF NOT EXISTS normative_fragments_search_idx ON normative_fragments USING GIN (search_vector);

COMMIT;
