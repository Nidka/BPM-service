-- Introducción vista por usuario y contacto de soporte de la plataforma.
-- Aditiva: agrega una columna opcional a application_users y crea platform_settings con una sola fila
-- (correo de soporte por defecto y WhatsApp opcional). No reescribe ni borra datos. Aplicar con ON_ERROR_STOP=1
-- antes de desplegar la API: la API falla al arrancar si falta (`platform settings migration V22 is required`).
BEGIN;
ALTER TABLE application_users ADD COLUMN IF NOT EXISTS onboarding_seen_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS platform_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    support_email VARCHAR(254) NOT NULL,
    support_whatsapp VARCHAR(20),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT,
    CONSTRAINT platform_settings_support_whatsapp_digits CHECK (support_whatsapp IS NULL OR support_whatsapp ~ '^[0-9]{8,15}$')
);
INSERT INTO platform_settings (id, support_email) VALUES (1, 'proyectobpmupn@gmail.com') ON CONFLICT (id) DO NOTHING;
COMMIT;
