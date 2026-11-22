-- RF-48 (opcional): webhooks salientes para automatizaciones (n8n u otra herramienta). El ADMIN de la
-- empresa registra URL HTTPS; el worker les envía las alertas firmadas con HMAC-SHA256. El secreto se guarda
-- para firmar y solo se muestra al crearlo o rotarlo.
BEGIN;

CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id UUID PRIMARY KEY,
    organization_id VARCHAR(64) NOT NULL REFERENCES organizations(id),
    url VARCHAR(500) NOT NULL CHECK (url LIKE 'https://%'),
    secret VARCHAR(128) NOT NULL CHECK (char_length(secret) >= 32),
    event_types TEXT[] NOT NULL CHECK (cardinality(event_types) BETWEEN 1 AND 10),
    active BOOLEAN NOT NULL DEFAULT true,
    last_status INTEGER,
    last_error VARCHAR(200),
    last_delivery_at TIMESTAMPTZ,
    created_by VARCHAR(128) NOT NULL REFERENCES application_users(id),
    updated_by VARCHAR(128) NOT NULL REFERENCES application_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS webhook_endpoints_org_idx ON webhook_endpoints (organization_id) WHERE deleted_at IS NULL;

COMMIT;
