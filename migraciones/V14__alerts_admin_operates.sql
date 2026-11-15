-- El ADMIN también opera: recibe y ve el aviso de control diario pendiente de cualquier sede.
-- Solo cambia la autorización de CHECKLIST_MISSING; el resto de la función queda igual a V7.
CREATE OR REPLACE FUNCTION alert_recipient_authorized(p_org TEXT,p_alert UUID,p_uid TEXT) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM alerts a JOIN organizations o ON o.id=a.organization_id AND o.status='ACTIVE' JOIN application_users u ON u.id=a.recipient_user_id AND u.status='ACTIVE' JOIN organization_memberships m ON m.organization_id=a.organization_id AND m.user_id=u.id AND m.status='ACTIVE' LEFT JOIN locations l ON l.organization_id=a.organization_id AND l.id=a.location_id WHERE a.organization_id=p_org AND a.id=p_alert AND a.recipient_user_id=p_uid AND a.status<>'CANCELLED' AND (a.location_id IS NULL OR l.status='ACTIVE') AND (m.role='ADMIN' OR a.location_id IS NULL OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=p_org AND ml.user_id=p_uid AND ml.location_id=a.location_id)) AND CASE a.alert_type
 WHEN 'CHECKLIST_MISSING' THEN m.role IN ('ADMIN','CHEF') AND EXISTS(SELECT 1 FROM checklist_assignments x WHERE x.organization_id=p_org AND x.id::text=a.source_id AND x.location_id=a.location_id)
 WHEN 'TEMPERATURE_DANGER' THEN m.role IN ('ADMIN','CHEF') AND EXISTS(SELECT 1 FROM supply_receipt_items i JOIN supply_receipts r ON r.organization_id=i.organization_id AND r.id=i.receipt_id WHERE i.organization_id=p_org AND i.id::text=a.source_id AND r.location_id=a.location_id)
 WHEN 'ORGANIZATION_INACTIVE' THEN m.role='ADMIN' AND a.source_id=p_org AND a.location_id IS NULL
 WHEN 'TRAINING_ASSIGNED' THEN m.role IN ('CHEF','WORKER') AND EXISTS(SELECT 1 FROM training_assignments x WHERE x.organization_id=p_org AND x.id::text=a.source_id AND (x.location_id IS NOT DISTINCT FROM a.location_id) AND (x.target_user_id IS NULL OR x.target_user_id=p_uid) AND (x.target_role IS NULL OR x.target_role=m.role))
 ELSE FALSE END)
$$;
