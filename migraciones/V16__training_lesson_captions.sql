-- RF-45 (accesibilidad): subtítulos WebVTT por lección. Texto vacío = sin subtítulos.
-- El formato y el contenido se validan en el dominio; aquí solo se acota el tamaño.
BEGIN;

ALTER TABLE training_lessons
 ADD COLUMN IF NOT EXISTS captions_vtt TEXT NOT NULL DEFAULT '',
 ADD CONSTRAINT training_lessons_captions_vtt_length_check CHECK (char_length(captions_vtt) <= 100000);

COMMIT;
