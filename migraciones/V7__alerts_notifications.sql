-- Alertas personales y entrega transaccional; aditiva sobre V1/V2/V4/V5/V6.
BEGIN;
ALTER TABLE alert_rules ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK(row_version>0), ADD COLUMN email_enabled BOOLEAN NOT NULL DEFAULT FALSE, ADD COLUMN push_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE alerts ADD COLUMN rule_id UUID, ADD COLUMN rule_version INTEGER, ADD COLUMN dedup_key TEXT, ADD COLUMN context_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb, ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK(row_version>0), ADD CONSTRAINT alerts_rule_fk FOREIGN KEY(organization_id,rule_id) REFERENCES alert_rules(organization_id,id), ADD CONSTRAINT alerts_dedup_uq UNIQUE(organization_id,dedup_key);
ALTER TABLE notification_deliveries ADD COLUMN target_key VARCHAR(128) NOT NULL DEFAULT 'legacy', ADD COLUMN target_device_id UUID, ADD COLUMN request_payload JSONB, ADD COLUMN first_attempt_at TIMESTAMPTZ, ADD COLUMN last_attempt_at TIMESTAMPTZ, ADD COLUMN next_attempt_at TIMESTAMPTZ, ADD COLUMN lease_token UUID, ADD COLUMN locked_until TIMESTAMPTZ;
-- Fallar explícitamente ante duplicados legados; nunca borrar historial silenciosamente.
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_target_uq UNIQUE(organization_id,alert_id,channel,target_key), ADD CONSTRAINT notification_deliveries_device_fk FOREIGN KEY(organization_id,target_device_id) REFERENCES mobile_devices(organization_id,id);
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_status_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_status_check CHECK(status IN ('PENDING','PROCESSING','SENT','DELIVERED','FAILED','CANCELLED','UNCERTAIN'));
ALTER TABLE mobile_devices ADD COLUMN token_hash VARCHAR(64);
CREATE UNIQUE INDEX mobile_devices_active_token_uq ON mobile_devices(organization_id,token_hash) WHERE status='ACTIVE' AND token_hash IS NOT NULL;
ALTER TABLE outbox_events ADD COLUMN dedup_key TEXT, ADD COLUMN lease_token UUID, ADD COLUMN locked_until TIMESTAMPTZ, ADD COLUMN expires_at TIMESTAMPTZ, ADD CONSTRAINT outbox_events_dedup_uq UNIQUE(organization_id,dedup_key);
CREATE INDEX alerts_personal_page_idx ON alerts(organization_id,recipient_user_id,created_at DESC,id);
CREATE INDEX alert_rules_scope_idx ON alert_rules(organization_id,location_id,active,id);
CREATE INDEX outbox_events_claim_idx ON outbox_events(available_at,created_at,id) WHERE status IN ('PENDING','PROCESSING');
CREATE INDEX notification_deliveries_claim_idx ON notification_deliveries(next_attempt_at,locked_until) WHERE status IN ('PENDING','PROCESSING');
-- Alcance histórico: no depende de que la regla siga activa ni del envío externo.
CREATE FUNCTION alert_recipient_authorized(p_org TEXT,p_alert UUID,p_uid TEXT) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM alerts a JOIN organizations o ON o.id=a.organization_id AND o.status='ACTIVE' JOIN application_users u ON u.id=a.recipient_user_id AND u.status='ACTIVE' JOIN organization_memberships m ON m.organization_id=a.organization_id AND m.user_id=u.id AND m.status='ACTIVE' LEFT JOIN locations l ON l.organization_id=a.organization_id AND l.id=a.location_id WHERE a.organization_id=p_org AND a.id=p_alert AND a.recipient_user_id=p_uid AND a.status<>'CANCELLED' AND (a.location_id IS NULL OR l.status='ACTIVE') AND (m.role='ADMIN' OR a.location_id IS NULL OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=p_org AND ml.user_id=p_uid AND ml.location_id=a.location_id)) AND CASE a.alert_type
 WHEN 'CHECKLIST_MISSING' THEN m.role='CHEF' AND EXISTS(SELECT 1 FROM checklist_assignments x WHERE x.organization_id=p_org AND x.id::text=a.source_id AND x.location_id=a.location_id)
 WHEN 'TEMPERATURE_DANGER' THEN m.role IN ('ADMIN','CHEF') AND EXISTS(SELECT 1 FROM supply_receipt_items i JOIN supply_receipts r ON r.organization_id=i.organization_id AND r.id=i.receipt_id WHERE i.organization_id=p_org AND i.id::text=a.source_id AND r.location_id=a.location_id)
 WHEN 'ORGANIZATION_INACTIVE' THEN m.role='ADMIN' AND a.source_id=p_org AND a.location_id IS NULL
 WHEN 'TRAINING_ASSIGNED' THEN m.role IN ('CHEF','WORKER') AND EXISTS(SELECT 1 FROM training_assignments x WHERE x.organization_id=p_org AND x.id::text=a.source_id AND (x.location_id IS NOT DISTINCT FROM a.location_id) AND (x.target_user_id IS NULL OR x.target_user_id=p_uid) AND (x.target_role IS NULL OR x.target_role=m.role))
 ELSE FALSE END)
$$;
-- Vigencia de la fuente: se usa inmediatamente antes del intento externo.
CREATE FUNCTION alert_source_pending(p_org TEXT,p_alert UUID) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 SELECT COALESCE((SELECT CASE a.alert_type
 WHEN 'CHECKLIST_MISSING' THEN EXISTS(SELECT 1 FROM checklist_assignments x JOIN checklist_templates t ON t.organization_id=x.organization_id AND t.id=x.template_id WHERE x.organization_id=p_org AND x.id::text=a.source_id AND x.active AND x.frequency='DAILY' AND t.status='ACTIVE' AND EXISTS(SELECT 1 FROM checklist_template_versions v WHERE v.organization_id=p_org AND v.template_id=t.id AND v.status='PUBLISHED') AND NOT EXISTS(SELECT 1 FROM checklist_runs r WHERE r.organization_id=p_org AND r.assignment_id=x.id AND r.status='SUBMITTED' AND r.created_at >= (a.context_snapshot->>'periodStart')::timestamptz AND r.created_at < (a.context_snapshot->>'periodEnd')::timestamptz))
 WHEN 'TEMPERATURE_DANGER' THEN EXISTS(SELECT 1 FROM supply_receipt_items i WHERE i.organization_id=p_org AND i.id::text=a.source_id AND i.temperature_celsius IS NOT NULL AND i.temperature_celsius=(a.context_snapshot->>'temperatureCelsius')::numeric AND i.temperature_celsius BETWEEN (a.context_snapshot->'configuration'->>'minimumTemperatureCelsius')::numeric AND (a.context_snapshot->'configuration'->>'maximumTemperatureCelsius')::numeric)
 WHEN 'ORGANIZATION_INACTIVE' THEN NOT EXISTS(SELECT 1 FROM (SELECT created_at FROM supply_receipts WHERE organization_id=p_org UNION ALL SELECT created_at FROM cleaning_executions WHERE organization_id=p_org UNION ALL SELECT created_at FROM incidents WHERE organization_id=p_org UNION ALL SELECT submitted_at FROM checklist_runs WHERE organization_id=p_org AND status='SUBMITTED') v WHERE v.created_at > (a.context_snapshot->>'lastActivity')::timestamptz)
 WHEN 'TRAINING_ASSIGNED' THEN EXISTS(SELECT 1 FROM training_assignments x JOIN training_courses c ON c.organization_id=x.organization_id AND c.id=x.course_id WHERE x.organization_id=p_org AND x.id::text=a.source_id AND x.status='ACTIVE' AND c.status='PUBLISHED')
 ELSE FALSE END FROM alerts a WHERE a.organization_id=p_org AND a.id=p_alert AND a.status<>'CANCELLED'),FALSE)
$$;
COMMIT;
