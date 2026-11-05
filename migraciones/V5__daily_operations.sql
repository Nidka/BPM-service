-- Criterios configurables y snapshots de operación diaria; el legado conserva NULL.
BEGIN;

ALTER TABLE products
    ADD COLUMN minimum_temperature NUMERIC(6,2),
    ADD COLUMN maximum_temperature NUMERIC(6,2),
    ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1,
    ADD CONSTRAINT products_row_version_check CHECK (row_version > 0),
    ADD CONSTRAINT products_temperature_policy_check CHECK (
        (minimum_temperature IS NULL AND maximum_temperature IS NULL) OR
        (requires_temperature AND minimum_temperature IS NOT NULL AND maximum_temperature IS NOT NULL
         AND minimum_temperature <= maximum_temperature)
    );

ALTER TABLE supply_receipt_items
    ADD COLUMN packaging_condition VARCHAR(20),
    ADD COLUMN display_order INTEGER,
    ADD CONSTRAINT supply_receipt_items_display_order_check CHECK (display_order IS NULL OR display_order BETWEEN 1 AND 50),
    ADD COLUMN minimum_temperature NUMERIC(6,2),
    ADD COLUMN maximum_temperature NUMERIC(6,2),
    ADD COLUMN storage_location VARCHAR(120),
    ADD COLUMN label VARCHAR(160),
    ADD CONSTRAINT supply_receipt_items_packaging_check CHECK (packaging_condition IS NULL OR packaging_condition IN ('INTACT','DAMAGED')),
    ADD CONSTRAINT supply_receipt_items_temperature_snapshot_check CHECK (
        (minimum_temperature IS NULL AND maximum_temperature IS NULL) OR
        (minimum_temperature IS NOT NULL AND maximum_temperature IS NOT NULL AND minimum_temperature <= maximum_temperature)
    );

ALTER TABLE supply_receipts ADD COLUMN request_hash VARCHAR(64),
    ADD CONSTRAINT supply_receipts_request_hash_check CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$');
ALTER TABLE cleaning_executions ADD COLUMN request_hash VARCHAR(64),
    ADD CONSTRAINT cleaning_executions_request_hash_check CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$');

CREATE UNIQUE INDEX storage_lots_receipt_item_uq ON storage_lots (organization_id, receipt_item_id);
CREATE INDEX operations_receipts_page_idx ON supply_receipts (organization_id, location_id, received_at DESC, id DESC);
CREATE INDEX operations_executions_page_idx ON cleaning_executions (organization_id, location_id, executed_at DESC, id DESC);
CREATE INDEX operations_worker_executions_page_idx ON cleaning_executions (organization_id, location_id, responsible_user_id, executed_at DESC, id DESC);
CREATE INDEX operations_plans_page_idx ON cleaning_plans (organization_id, location_id, (LOWER(area || ' ' || task) COLLATE "C"), id);
CREATE INDEX operations_products_page_idx ON products (organization_id, (LOWER(name) COLLATE "C"), id) WHERE status='ACTIVE';
CREATE INDEX operations_suppliers_page_idx ON suppliers (organization_id, (LOWER(name) COLLATE "C"), id) WHERE status='ACTIVE';
CREATE INDEX operations_receipt_items_idx ON supply_receipt_items (organization_id, receipt_id, id);

COMMIT;
