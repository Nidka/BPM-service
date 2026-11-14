BEGIN;

-- Operaciones ampliadas: turnos por sede, movimientos de stock y versiones de plantillas.
-- Preflight: se detiene si datos existentes violarían una nueva unicidad. Nunca borra ni fusiona datos.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM checklist_template_versions WHERE status = 'PUBLISHED' GROUP BY organization_id, template_id HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'V13 preflight: hay plantillas con más de una versión PUBLISHED';
 END IF;
END
$$;

-- Turnos configurables de una sede. Las horas son locales (zona horaria de la sede);
-- un fin menor que el inicio indica que el turno cruza la medianoche.
CREATE TABLE location_shifts (
    id UUID PRIMARY KEY,
    organization_id VARCHAR(64) NOT NULL,
    location_id UUID NOT NULL,
    name VARCHAR(60) NOT NULL,
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    row_version INTEGER NOT NULL DEFAULT 1,
    request_hash VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, location_id) REFERENCES locations(organization_id, id),
    CONSTRAINT location_shifts_hours_check CHECK (start_time <> end_time),
    CONSTRAINT location_shifts_version_check CHECK (row_version > 0),
    CONSTRAINT location_shifts_hash_check CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$')
);
CREATE UNIQUE INDEX location_shifts_active_name_uq ON location_shifts (organization_id, location_id, (LOWER(name))) WHERE active;
CREATE INDEX location_shifts_location_idx ON location_shifts (organization_id, location_id, active, start_time, id);
CREATE TRIGGER location_shifts_set_updated_at BEFORE UPDATE ON location_shifts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Programación y registros por turno. shift_id nulo conserva el comportamiento anterior (sin turno).
ALTER TABLE checklist_assignments ADD COLUMN shift_id UUID,
    ADD CONSTRAINT checklist_assignments_shift_fk FOREIGN KEY (organization_id, shift_id) REFERENCES location_shifts(organization_id, id),
    ADD CONSTRAINT checklist_assignments_shift_frequency_check CHECK (shift_id IS NULL OR (frequency = 'DAILY' AND due_time IS NULL));
CREATE UNIQUE INDEX checklist_assignments_active_shift_uq ON checklist_assignments (organization_id, location_id, template_id, shift_id) WHERE active AND shift_id IS NOT NULL;
ALTER TABLE checklist_runs ADD COLUMN shift_id UUID,
    ADD CONSTRAINT checklist_runs_shift_fk FOREIGN KEY (organization_id, shift_id) REFERENCES location_shifts(organization_id, id);
ALTER TABLE cleaning_executions ADD COLUMN shift_id UUID,
    ADD CONSTRAINT cleaning_executions_shift_fk FOREIGN KEY (organization_id, shift_id) REFERENCES location_shifts(organization_id, id);
CREATE INDEX checklist_assignments_shift_idx ON checklist_assignments (organization_id, shift_id) WHERE shift_id IS NOT NULL;

-- Una sola versión vigente por plantilla; al publicar otra, la anterior pasa a ARCHIVED.
-- Las ejecuciones conservan su template_version_id, por lo que nunca cambian de versión.
ALTER TABLE checklist_template_versions ADD COLUMN change_note VARCHAR(500),
    ADD COLUMN request_hash VARCHAR(64),
    ADD CONSTRAINT checklist_template_versions_hash_check CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$');
CREATE UNIQUE INDEX checklist_versions_one_published_uq ON checklist_template_versions (organization_id, template_id) WHERE status = 'PUBLISHED';

-- Movimientos de stock sobre lotes: consumo o descarte, inmutables e idempotentes por UUID.
CREATE TABLE stock_movements (
    id UUID PRIMARY KEY,
    organization_id VARCHAR(64) NOT NULL,
    location_id UUID NOT NULL,
    storage_lot_id UUID NOT NULL,
    movement_type VARCHAR(20) NOT NULL,
    quantity NUMERIC(12, 3) NOT NULL,
    balance_after NUMERIC(12, 3) NOT NULL,
    reason VARCHAR(20),
    note VARCHAR(500),
    performed_by VARCHAR(128) NOT NULL,
    client_operation_id UUID NOT NULL,
    client_created_at TIMESTAMPTZ NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, client_operation_id),
    FOREIGN KEY (organization_id, location_id) REFERENCES locations(organization_id, id),
    FOREIGN KEY (organization_id, storage_lot_id) REFERENCES storage_lots(organization_id, id),
    FOREIGN KEY (organization_id, performed_by) REFERENCES organization_memberships(organization_id, user_id),
    CONSTRAINT stock_movements_type_check CHECK (movement_type IN ('CONSUMPTION', 'DISCARD')),
    CONSTRAINT stock_movements_quantity_check CHECK (quantity > 0),
    CONSTRAINT stock_movements_balance_check CHECK (balance_after >= 0),
    CONSTRAINT stock_movements_reason_check CHECK (
        (movement_type = 'CONSUMPTION' AND reason IS NULL) OR
        (movement_type = 'DISCARD' AND reason IN ('EXPIRED', 'DETERIORATED', 'CONTAMINATION', 'OTHER'))),
    CONSTRAINT stock_movements_other_note_check CHECK (reason IS DISTINCT FROM 'OTHER' OR note IS NOT NULL),
    CONSTRAINT stock_movements_hash_check CHECK (request_hash ~ '^[0-9a-f]{64}$')
);
CREATE INDEX stock_movements_location_page_idx ON stock_movements (organization_id, location_id, created_at DESC, id DESC);
CREATE INDEX stock_movements_lot_page_idx ON stock_movements (organization_id, location_id, storage_lot_id, created_at DESC, id DESC);

-- El historial de stock es un registro de hechos: no se edita ni se borra.
CREATE FUNCTION stock_movements_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'stock_movements is append-only';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER stock_movements_no_change BEFORE UPDATE OR DELETE ON stock_movements
    FOR EACH ROW EXECUTE FUNCTION stock_movements_immutable();

COMMIT;
