BEGIN;

-- Cada reporte conserva el criterio publicado que referenció al crearse.
ALTER TABLE incident_catalog ADD COLUMN version_number INTEGER NOT NULL DEFAULT 1 CHECK (version_number > 0);
ALTER TABLE incident_catalog DROP CONSTRAINT incident_catalog_organization_id_code_key;
ALTER TABLE incident_catalog ADD CONSTRAINT incident_catalog_code_version_uq UNIQUE (organization_id, code, version_number);
ALTER TABLE incidents ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0);
ALTER TABLE incidents ADD COLUMN request_hash VARCHAR(64) CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$');
ALTER TABLE corrective_actions ADD COLUMN resolution_note TEXT;

-- response_body es un recibo técnico privado: nunca se proyecta en el historial público.
CREATE TABLE incident_events (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 organization_id VARCHAR(64) NOT NULL REFERENCES organizations(id),
 incident_id UUID NOT NULL,
 operation_id UUID NOT NULL,
 from_status VARCHAR(24),
 to_status VARCHAR(24) NOT NULL,
 actor_user_id VARCHAR(128) NOT NULL REFERENCES application_users(id),
 note TEXT NOT NULL DEFAULT '',
 request_hash VARCHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
 response_body JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE (organization_id, operation_id),
 FOREIGN KEY (organization_id, incident_id) REFERENCES incidents(organization_id,id),
 CHECK (from_status IS NULL OR from_status IN ('OPEN','ASSIGNED','IN_PROGRESS','RESOLVED','VERIFIED','CLOSED')),
 CHECK (to_status IN ('OPEN','ASSIGNED','IN_PROGRESS','RESOLVED','VERIFIED','CLOSED'))
);
CREATE INDEX incident_catalog_latest_idx ON incident_catalog(organization_id,code,version_number DESC);
CREATE INDEX incidents_page_idx ON incidents(organization_id,location_id,created_at DESC,id);
CREATE INDEX incident_events_page_idx ON incident_events(organization_id,incident_id,created_at DESC,id);
CREATE INDEX corrective_actions_incident_idx ON corrective_actions(organization_id,incident_id);
CREATE INDEX incident_media_stage_idx ON incident_media(organization_id,incident_id,stage,corrective_action_id);

-- La aplicación publica nuevas versiones y agrega eventos; nadie reescribe historia.
CREATE FUNCTION reject_incident_history_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'incident history is immutable' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER incident_events_append_only BEFORE UPDATE OR DELETE ON incident_events FOR EACH ROW EXECUTE FUNCTION reject_incident_history_change();
CREATE TRIGGER incident_catalog_immutable BEFORE UPDATE OR DELETE ON incident_catalog FOR EACH ROW EXECUTE FUNCTION reject_incident_history_change();

COMMIT;
