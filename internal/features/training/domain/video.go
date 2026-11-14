package domain

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"io"
	"regexp"
	"strings"
	"time"
)

// Formatos de video admitidos en la Academia BPM.
const (
	MIMEVideoMP4  = "video/mp4"
	MIMEVideoWebM = "video/webm"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VideoPolicy fija el límite de tamaño vigente (TRAINING_VIDEO_MAX_BYTES).
type VideoPolicy struct{ MaxBytes int64 }

// VideoUploadInput son los metadatos inmutables que el cliente declara al reservar.
type VideoUploadInput struct {
	ID, OriginalName, MIMEType, SHA256 string
	SizeBytes                          int64
}

// VideoFile es el registro de un video de capacitación en media_files.
type VideoFile struct {
	ID, OrganizationID, ObjectKey, OriginalName, MIMEType, SHA256, Status, CreatedBy string
	SizeBytes                                                                        int64
	CreatedAt                                                                        time.Time
	UploadedAt                                                                       *time.Time
}

// Validate normaliza y valida la reserva: solo MP4 o WebM, tamaño acotado y SHA-256 hexadecimal.
func (p VideoPolicy) Validate(in VideoUploadInput) (VideoUploadInput, error) {
	fields := map[string]string{}
	in.ID = strings.ToLower(in.ID)
	in.OriginalName = strings.TrimSpace(in.OriginalName)
	in.MIMEType = strings.ToLower(strings.TrimSpace(in.MIMEType))
	in.SHA256 = strings.ToLower(in.SHA256)
	if !ValidID(in.ID) {
		fields["id"] = "El video necesita un identificador UUID."
	}
	if !validText(in.OriginalName, 1, 200, false) || strings.ContainsAny(in.OriginalName, `/\`) {
		fields["originalName"] = "Usa un nombre de archivo sin rutas ni caracteres especiales."
	}
	if in.MIMEType != MIMEVideoMP4 && in.MIMEType != MIMEVideoWebM {
		fields["mimeType"] = "Solo se admiten videos MP4 o WebM."
	}
	if in.SizeBytes < 1 || in.SizeBytes > p.MaxBytes {
		fields["sizeBytes"] = "El video está vacío o supera el tamaño permitido."
	}
	if !sha256Pattern.MatchString(in.SHA256) {
		fields["sha256"] = "No pudimos comprobar la integridad del video."
	}
	return in, invalid(fields)
}

// VideoExtension devuelve la extensión de la clave para un MIME admitido.
func VideoExtension(mime string) string {
	if mime == MIMEVideoWebM {
		return "webm"
	}
	return "mp4"
}

// VideoObjectKey construye organizations/{org}/training/videos/{id}.{ext}.
func VideoObjectKey(organizationID, id, mime string) string {
	return "organizations/" + organizationID + "/training/videos/" + id + "." + VideoExtension(mime)
}

// Match verifica que una repetición declare exactamente los mismos metadatos.
func (f VideoFile) Match(in VideoUploadInput) error {
	if f.ID != in.ID || f.OriginalName != in.OriginalName || f.MIMEType != in.MIMEType || f.SizeBytes != in.SizeBytes || f.SHA256 != in.SHA256 {
		return ErrVideoConflict
	}
	return nil
}

// ValidVideoSignature comprueba la firma real del formato en los primeros bytes:
// MP4 contiene "ftyp" en el desplazamiento 4; WebM inicia con 1A 45 DF A3 (EBML).
func ValidVideoSignature(mime string, header []byte) bool {
	switch mime {
	case MIMEVideoMP4:
		return len(header) >= 8 && bytes.Equal(header[4:8], []byte("ftyp"))
	case MIMEVideoWebM:
		return len(header) >= 4 && bytes.Equal(header[:4], []byte{0x1A, 0x45, 0xDF, 0xA3})
	}
	return false
}

var verificationEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewVerificationCode genera un código aleatorio criptográfico de 24 caracteres
// base32 (120 bits). random suele ser crypto/rand.Reader; las pruebas inyectan otro.
func NewVerificationCode(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, 15)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return verificationEncoding.EncodeToString(raw), nil
}
