BEGIN;

-- Gestión de cuentas (RF-04, RF-49): control optimista, invitaciones por correo
-- y tipo de establecimiento ampliado. Aditiva sobre V1; las bajas son lógicas.

-- Preflight: los tipos de establecimiento solo pueden ser los conocidos.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM organizations WHERE business_type NOT IN ('RESTAURANT','CAFETERIA','CATERING','RELATED_SERVICE','OTHER_FOOD_SERVICE')) THEN
  RAISE EXCEPTION 'V12 preflight: hay organizaciones con un tipo de establecimiento desconocido';
 END IF;
END
$$;

-- RF-49: RELATED_SERVICE (servicio relacionado) se migra al valor por defecto
-- documentado OTHER_FOOD_SERVICE; RESTAURANT, CAFETERIA y CATERING no cambian.
ALTER TABLE organizations DROP CONSTRAINT organizations_business_type_check;
UPDATE organizations SET business_type = 'OTHER_FOOD_SERVICE' WHERE business_type = 'RELATED_SERVICE';
ALTER TABLE organizations ADD CONSTRAINT organizations_business_type_check
 CHECK (business_type IN ('RESTAURANT', 'CAFETERIA', 'CATERING', 'OTHER_FOOD_SERVICE'));

-- Control de concurrencia optimista de las entidades editables.
ALTER TABLE organizations ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0);
ALTER TABLE locations ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0);
ALTER TABLE organization_memberships ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0);

-- Invitaciones por correo. Solo se guarda el hash SHA-256 del token; el token
-- sin cifrar viaja una vez en el evento de correo y se borra al enviarse.
CREATE TABLE organization_invitations (
    id UUID PRIMARY KEY,
    organization_id VARCHAR(64) NOT NULL REFERENCES organizations(id),
    email VARCHAR(320) NOT NULL,
    role VARCHAR(20) NOT NULL,
    location_ids UUID[] NOT NULL DEFAULT '{}',
    token_hash VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    expires_at TIMESTAMPTZ NOT NULL,
    send_count INTEGER NOT NULL DEFAULT 1 CHECK (send_count > 0),
    last_sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_by VARCHAR(128),
    accepted_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    updated_by VARCHAR(128),
    UNIQUE (organization_id, id),
    CONSTRAINT organization_invitations_role_check CHECK (role IN ('ADMIN', 'CHEF', 'WORKER')),
    CONSTRAINT organization_invitations_status_check CHECK (status IN ('PENDING', 'ACCEPTED', 'REVOKED', 'EXPIRED')),
    CONSTRAINT organization_invitations_token_hash_check CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT organization_invitations_locations_check CHECK (role = 'ADMIN' OR cardinality(location_ids) > 0)
);
CREATE UNIQUE INDEX organization_invitations_token_uq ON organization_invitations (token_hash);
-- Una sola invitación pendiente por correo y organización.
CREATE UNIQUE INDEX organization_invitations_pending_uq ON organization_invitations (organization_id, LOWER(email)) WHERE status = 'PENDING';
CREATE INDEX organization_invitations_page_idx ON organization_invitations (organization_id, LOWER(email), id);

CREATE INDEX mobile_devices_user_page_idx ON mobile_devices (user_id, created_at, id) WHERE status = 'ACTIVE';

COMMIT;
