-- Videos de la plataforma: los videos de los módulos BPM del catálogo base son los mismos para todas las
-- organizaciones, así que no pertenecen a ninguna (media_files exige organization_id). Una lección puede referirse a
-- uno con platform_video_id. El objeto vive en R2 bajo organizations/plataforma/training/videos/ (la misma forma de
-- clave que los videos privados, sin cambiar el adaptador). Aditiva: una tabla nueva y una columna opcional.
BEGIN;

CREATE TABLE IF NOT EXISTS platform_training_videos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(60) NOT NULL UNIQUE,
    object_key VARCHAR(500) NOT NULL UNIQUE,
    original_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'PENDING_UPLOAD',
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT platform_training_videos_mime_check CHECK (mime_type IN ('video/mp4', 'video/webm')),
    CONSTRAINT platform_training_videos_size_check CHECK (size_bytes > 0),
    CONSTRAINT platform_training_videos_sha_check CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT platform_training_videos_status_check CHECK (status IN ('PENDING_UPLOAD', 'AVAILABLE', 'FAILED')),
    CONSTRAINT platform_training_videos_key_check CHECK (starts_with(object_key, 'organizations/plataforma/training/videos/'))
);
CREATE TRIGGER platform_training_videos_set_updated_at BEFORE UPDATE ON platform_training_videos FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE training_lessons
 ADD COLUMN IF NOT EXISTS platform_video_id UUID REFERENCES platform_training_videos(id),
 ADD CONSTRAINT training_lessons_single_platform_video_check CHECK (platform_video_id IS NULL OR (media_file_id IS NULL AND external_video_id IS NULL));
CREATE INDEX IF NOT EXISTS training_lessons_platform_video_idx ON training_lessons (platform_video_id) WHERE platform_video_id IS NOT NULL;

COMMIT;
