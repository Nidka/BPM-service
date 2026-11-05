-- La evidencia pertenece a la pregunta inmutable, no a una fila reemplazable de respuesta.
BEGIN;

ALTER TABLE checklist_media ADD COLUMN checklist_item_id UUID;
UPDATE checklist_media cm SET checklist_item_id = a.checklist_item_id
FROM checklist_answers a
WHERE a.organization_id = cm.organization_id AND a.id = cm.checklist_answer_id;
ALTER TABLE checklist_media ADD CONSTRAINT checklist_media_item_fk
    FOREIGN KEY (organization_id, checklist_item_id) REFERENCES checklist_items(organization_id, id);
ALTER TABLE checklist_media DROP COLUMN checklist_answer_id;
CREATE INDEX checklist_media_item_idx ON checklist_media (organization_id, checklist_run_id, checklist_item_id);

COMMIT;
