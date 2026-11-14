package contracts

import (
	"context"
)

// VideoHeaderBytes es la cantidad de bytes iniciales que InspectVideo devuelve
// para que la feature compruebe la firma del formato (MP4 o WebM).
const VideoHeaderBytes = 12

// VideoInspection resume el objeto leído en streaming desde el almacenamiento.
type VideoInspection struct {
	ContentType   string
	ContentLength int64
	SHA256Hex     string
	// Header contiene los primeros VideoHeaderBytes bytes reales del objeto.
	Header []byte
}

// VideoStorage es el puerto técnico para videos privados de capacitación.
// Se mantiene separado de ObjectStorage porque los límites (tamaño, tiempo de
// firma e inspección) son distintos de los de las fotos de evidencia.
type VideoStorage interface {
	// PresignVideoUpload firma un PUT sin sobrescritura para la clave dada.
	PresignVideoUpload(ctx context.Context, request UploadRequest) (SignedRequest, error)
	// PresignVideoDownload firma un GET temporal para la clave dada.
	PresignVideoDownload(ctx context.Context, objectKey string) (SignedRequest, error)
	// InspectVideo calcula el SHA-256 de todo el objeto en streaming, sin cargarlo
	// en memoria, con tiempo acotado, y devuelve la cabecera para validar el formato.
	InspectVideo(ctx context.Context, objectKey string) (VideoInspection, error)
}
