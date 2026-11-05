package contracts

import (
	"context"
	"errors"
	"time"
)

var ErrObjectNotFound = errors.New("private object not found")
var ErrInvalidObject = errors.New("invalid private object content")

type UploadRequest struct {
	ObjectKey     string
	ContentType   string
	ContentLength int64
	SHA256Hex     string
}

type SignedRequest struct {
	URL       string
	Headers   map[string][]string
	ExpiresAt time.Time
}

type ObjectMetadata struct {
	ContentType   string
	ContentLength int64
	SHA256Hex     string
}

// ObjectStorage is the outbound port for private evidence files.
type ObjectStorage interface {
	PresignUpload(ctx context.Context, request UploadRequest) (SignedRequest, error)
	PresignDownload(ctx context.Context, objectKey string) (SignedRequest, error)
	Head(ctx context.Context, objectKey string) (ObjectMetadata, error)
	// Inspect calcula la huella sobre los bytes y valida el formato de la imagen.
	Inspect(ctx context.Context, objectKey string) (ObjectMetadata, error)
}
