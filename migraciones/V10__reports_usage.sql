-- Reportes, constancias de cumplimiento, correo semanal y registro de uso (RF-09, 16, 17, 27, 28, 29, 30, 39).
-- Aditiva sobre V1/V2/V4..V8. No crea datos de prueba ni modifica filas existentes.
BEGIN;

-- Resuelve la zona horaria de una sede: si el nombre no existe en PostgreSQL se usa America/Lima.
CREATE FUNCTION bpm_report_timezone(p_zone TEXT) RETURNS TEXT LANGUAGE SQL STABLE AS $$
 SELECT COALESCE((SELECT name FROM pg_timezone_names WHERE name = p_zone LIMIT 1), 'America/Lima')
$$;

-- Configuración de reportes por empresa. Sin fila se aplican los valores por defecto del servidor
-- (correo semanal activo y umbral 80 %).
CREATE TABLE report_settings (
    organization_id VARCHAR(64) PRIMARY KEY REFERENCES organizations(id),
    weekly_email_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    compliance_threshold NUMERIC(5,2) NOT NULL DEFAULT 80,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    CONSTRAINT report_settings_threshold_check CHECK (compliance_threshold BETWEEN 1 AND 100)
);

-- Constancias internas de cumplimiento: inmutables y verificables por código aleatorio.
CREATE TABLE compliance_certificates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id VARCHAR(64) NOT NULL REFERENCES organizations(id),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    average_compliance NUMERIC(5,2) NOT NULL,
    threshold NUMERIC(5,2) NOT NULL,
    runs_count INTEGER NOT NULL,
    verification_code VARCHAR(32) NOT NULL,
    issued_by VARCHAR(128) NOT NULL REFERENCES application_users(id),
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, period_start, period_end),
    UNIQUE (verification_code),
    CONSTRAINT compliance_certificates_period_check CHECK (period_end >= period_start),
    CONSTRAINT compliance_certificates_average_check CHECK (average_compliance BETWEEN 0 AND 100),
    CONSTRAINT compliance_certificates_threshold_check CHECK (threshold BETWEEN 1 AND 100 AND average_compliance >= threshold),
    CONSTRAINT compliance_certificates_runs_check CHECK (runs_count > 0),
    CONSTRAINT compliance_certificates_code_check CHECK (verification_code ~ '^[A-Z2-7]{24}$')
);
CREATE INDEX compliance_certificates_page_idx ON compliance_certificates (organization_id, issued_at DESC, id);

CREATE FUNCTION reject_compliance_certificate_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'compliance certificates are immutable' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER compliance_certificates_immutable BEFORE UPDATE OR DELETE ON compliance_certificates
 FOR EACH ROW EXECUTE FUNCTION reject_compliance_certificate_change();
CREATE TRIGGER compliance_certificates_no_truncate BEFORE TRUNCATE ON compliance_certificates
 FOR EACH STATEMENT EXECUTE FUNCTION reject_compliance_certificate_change();

-- Entregas del correo semanal: una por empresa, semana (lunes) y administrador. El cuerpo es un
-- resumen agregado; el correo del destinatario se lee de application_users al enviar.
CREATE TABLE weekly_report_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id VARCHAR(64) NOT NULL REFERENCES organizations(id),
    week_start DATE NOT NULL,
    recipient_user_id VARCHAR(128) NOT NULL REFERENCES application_users(id),
    subject VARCHAR(200) NOT NULL,
    body_text TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    locked_until TIMESTAMPTZ,
    provider_message_id VARCHAR(200),
    sent_at TIMESTAMPTZ,
    last_error_code VARCHAR(60),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, week_start, recipient_user_id),
    CONSTRAINT weekly_report_deliveries_status_check CHECK (status IN ('PENDING','PROCESSING','SENT','FAILED','CANCELLED')),
    CONSTRAINT weekly_report_deliveries_attempts_check CHECK (attempts >= 0),
    CONSTRAINT weekly_report_deliveries_week_check CHECK (EXTRACT(ISODOW FROM week_start) = 1)
);
CREATE INDEX weekly_report_deliveries_claim_idx ON weekly_report_deliveries (next_attempt_at, id) WHERE status IN ('PENDING','PROCESSING');

-- Idempotencia y límites del registro de uso. client_event_id es el UUID generado por el cliente;
-- las filas anteriores conservan NULL. El servidor nunca guarda metadatos del cliente.
ALTER TABLE usage_events ADD COLUMN client_event_id UUID;
CREATE UNIQUE INDEX usage_events_client_event_uq ON usage_events (organization_id, client_event_id) WHERE client_event_id IS NOT NULL;
ALTER TABLE usage_events ADD CONSTRAINT usage_events_event_name_check
    CHECK (event_name IN ('SESSION_START','SESSION_END','MODULE_VISIT','ALERT_VIEWED','ALERT_ATTENDED')) NOT VALID;
ALTER TABLE usage_events ADD CONSTRAINT usage_events_metadata_empty_check CHECK (metadata = '{}'::jsonb) NOT VALID;
ALTER TABLE usage_sessions ADD CONSTRAINT usage_sessions_period_check CHECK (ended_at IS NULL OR ended_at >= started_at) NOT VALID;
CREATE INDEX usage_events_session_idx ON usage_events (organization_id, session_id);

-- Índices de lectura de reportes.
CREATE INDEX checklist_runs_report_idx ON checklist_runs (organization_id, location_id, submitted_at) WHERE status = 'SUBMITTED';
CREATE INDEX incidents_report_idx ON incidents (organization_id, location_id, created_at);
CREATE INDEX supply_receipts_report_idx ON supply_receipts (organization_id, location_id, received_at);
CREATE INDEX training_progress_course_idx ON training_progress (organization_id, course_id, status);

COMMIT;
