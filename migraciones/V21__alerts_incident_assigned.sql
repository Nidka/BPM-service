-- Aviso «incidencia asignada» para el responsable de la acción correctiva (tipo INCIDENT_ASSIGNED, fuente INCIDENT).
-- Aditiva y sin cambios de tablas: alerts.alert_type y alerts.source_type son VARCHAR sin CHECK. Solo amplía las dos
-- funciones de autorización/vigencia (el tipo desconocido caía en ELSE FALSE) y publica una función marcadora que el
-- backend consulta para saber si puede emitir el aviso. Aplicar después de V14 (conserva el cambio de V14) con ON_ERROR_STOP=1.
-- Desplegar el backend antes de esta migración es seguro: sin la marca no emite el aviso y la asignación sigue igual.
BEGIN;
-- Marca de capacidad: el emisor de incidencias solo crea el aviso si esta función existe.
CREATE OR REPLACE FUNCTION alert_incident_assigned_supported() RETURNS BOOLEAN LANGUAGE SQL IMMUTABLE AS $$ SELECT TRUE $$;
-- Alcance histórico: el aviso lo ve solo el responsable de esa acción, con membresía y sede vigentes (V7/V14 + INCIDENT_ASSIGNED).
CREATE OR REPLACE FUNCTION alert_recipient_authorized(p_org TEXT,p_alert UUID,p_uid TEXT) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM alerts a JOIN organizations o ON o.id=a.organization_id AND o.status='ACTIVE' JOIN application_users u ON u.id=a.recipient_user_id AND u.status='ACTIVE' JOIN organization_memberships m ON m.organization_id=a.organization_id AND m.user_id=u.id AND m.status='ACTIVE' LEFT JOIN locations l ON l.organization_id=a.organization_id AND l.id=a.location_id WHERE a.organization_id=p_org AND a.id=p_alert AND a.recipient_user_id=p_uid AND a.status<>'CANCELLED' AND (a.location_id IS NULL OR l.status='ACTIVE') AND (m.role='ADMIN' OR a.location_id IS NULL OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=p_org AND ml.user_id=p_uid AND ml.location_id=a.location_id)) AND CASE a.alert_type
 WHEN 'CHECKLIST_MISSING' THEN m.role IN ('ADMIN','CHEF') AND EXISTS(SELECT 1 FROM checklist_assignments x WHERE x.organization_id=p_org AND x.id::text=a.source_id AND x.location_id=a.location_id)
 WHEN 'TEMPERATURE_DANGER' THEN m.role IN ('ADMIN','CHEF') AND EXISTS(SELECT 1 FROM supply_receipt_items i JOIN supply_receipts r ON r.organization_id=i.organization_id AND r.id=i.receipt_id WHERE i.organization_id=p_org AND i.id::text=a.source_id AND r.location_id=a.location_id)
 WHEN 'ORGANIZATION_INACTIVE' THEN m.role='ADMIN' AND a.source_id=p_org AND a.location_id IS NULL
 WHEN 'TRAINING_ASSIGNED' THEN m.role IN ('CHEF','WORKER') AND EXISTS(SELECT 1 FROM training_assignments x WHERE x.organization_id=p_org AND x.id::text=a.source_id AND (x.location_id IS NOT DISTINCT FROM a.location_id) AND (x.target_user_id IS NULL OR x.target_user_id=p_uid) AND (x.target_role IS NULL OR x.target_role=m.role))
 WHEN 'INCIDENT_ASSIGNED' THEN m.role IN ('ADMIN','CHEF','WORKER') AND a.source_type='INCIDENT' AND EXISTS(SELECT 1 FROM corrective_actions x WHERE x.organization_id=p_org AND x.incident_id::text=a.source_id AND x.id::text=a.context_snapshot->>'actionId' AND x.responsible_user_id=p_uid)
 ELSE FALSE END)
$$;
-- Vigencia de la fuente: el aviso sigue pendiente mientras la acción esté PENDING (sin iniciar) y la incidencia ASSIGNED.
CREATE OR REPLACE FUNCTION alert_source_pending(p_org TEXT,p_alert UUID) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 SELECT COALESCE((SELECT CASE a.alert_type
 WHEN 'CHECKLIST_MISSING' THEN EXISTS(SELECT 1 FROM checklist_assignments x JOIN checklist_templates t ON t.organization_id=x.organization_id AND t.id=x.template_id WHERE x.organization_id=p_org AND x.id::text=a.source_id AND x.active AND x.frequency='DAILY' AND t.status='ACTIVE' AND EXISTS(SELECT 1 FROM checklist_template_versions v WHERE v.organization_id=p_org AND v.template_id=t.id AND v.status='PUBLISHED') AND NOT EXISTS(SELECT 1 FROM checklist_runs r WHERE r.organization_id=p_org AND r.assignment_id=x.id AND r.status='SUBMITTED' AND r.created_at >= (a.context_snapshot->>'periodStart')::timestamptz AND r.created_at < (a.context_snapshot->>'periodEnd')::timestamptz))
 WHEN 'TEMPERATURE_DANGER' THEN EXISTS(SELECT 1 FROM supply_receipt_items i WHERE i.organization_id=p_org AND i.id::text=a.source_id AND i.temperature_celsius IS NOT NULL AND i.temperature_celsius=(a.context_snapshot->>'temperatureCelsius')::numeric AND i.temperature_celsius BETWEEN (a.context_snapshot->'configuration'->>'minimumTemperatureCelsius')::numeric AND (a.context_snapshot->'configuration'->>'maximumTemperatureCelsius')::numeric)
 WHEN 'ORGANIZATION_INACTIVE' THEN NOT EXISTS(SELECT 1 FROM (SELECT created_at FROM supply_receipts WHERE organization_id=p_org UNION ALL SELECT created_at FROM cleaning_executions WHERE organization_id=p_org UNION ALL SELECT created_at FROM incidents WHERE organization_id=p_org UNION ALL SELECT submitted_at FROM checklist_runs WHERE organization_id=p_org AND status='SUBMITTED') v WHERE v.created_at > (a.context_snapshot->>'lastActivity')::timestamptz)
 WHEN 'TRAINING_ASSIGNED' THEN EXISTS(SELECT 1 FROM training_assignments x JOIN training_courses c ON c.organization_id=x.organization_id AND c.id=x.course_id WHERE x.organization_id=p_org AND x.id::text=a.source_id AND x.status='ACTIVE' AND c.status='PUBLISHED')
 WHEN 'INCIDENT_ASSIGNED' THEN EXISTS(SELECT 1 FROM corrective_actions x JOIN incidents i ON i.organization_id=x.organization_id AND i.id=x.incident_id WHERE x.organization_id=p_org AND x.incident_id::text=a.source_id AND x.id::text=a.context_snapshot->>'actionId' AND x.responsible_user_id=a.recipient_user_id AND x.status='PENDING' AND i.status='ASSIGNED')
 ELSE FALSE END FROM alerts a WHERE a.organization_id=p_org AND a.id=p_alert AND a.status<>'CANCELLED'),FALSE)
$$;
COMMIT;
