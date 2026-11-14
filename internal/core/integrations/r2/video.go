package r2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	// videoURLTTL fija la vigencia de las firmas de video: 15 minutos, el máximo
	// permitido por SECURITY.md, porque una carga de cientos de MiB puede tardar.
	videoURLTTL = 15 * time.Minute
	// videoInspectTimeout acota la lectura completa del objeto para calcular el SHA-256.
	videoInspectTimeout = 2 * time.Minute
)

var allowedVideoMIMEs = map[string]struct{}{"video/mp4": {}, "video/webm": {}}

// validateVideoKey exige la ubicación privada de videos de capacitación:
// organizations/{org}/training/videos/{archivo}, sin retrocesos ni separadores.
func validateVideoKey(key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	parts := strings.Split(key, "/")
	if len(parts) != 5 || parts[1] == "" || parts[2] != "training" || parts[3] != "videos" || parts[4] == "" || path.Base(key) != parts[4] {
		return ErrInvalidObjectKey
	}
	return nil
}

// PresignVideoUpload firma un PUT sin sobrescritura para un video MP4 o WebM.
func (c *Client) PresignVideoUpload(ctx context.Context, request contracts.UploadRequest) (contracts.SignedRequest, error) {
	if err := validateVideoKey(request.ObjectKey); err != nil {
		return contracts.SignedRequest{}, err
	}
	contentType := strings.ToLower(strings.TrimSpace(request.ContentType))
	if _, allowed := allowedVideoMIMEs[contentType]; !allowed || request.ContentLength < 1 || request.ContentLength > c.videoMaxBytes {
		return contracts.SignedRequest{}, ErrInvalidMedia
	}
	if len(request.SHA256Hex) != sha256.Size*2 {
		return contracts.SignedRequest{}, ErrInvalidMedia
	}
	if _, err := hex.DecodeString(request.SHA256Hex); err != nil {
		return contracts.SignedRequest{}, ErrInvalidMedia
	}
	result, err := c.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(c.bucket),
		Key:                aws.String(request.ObjectKey),
		ContentType:        aws.String(contentType),
		ContentLength:      aws.Int64(request.ContentLength),
		IfNoneMatch:        aws.String("*"),
		CacheControl:       aws.String("private, no-store"),
		ContentDisposition: aws.String("inline"),
		Metadata:           map[string]string{"sha256": strings.ToLower(request.SHA256Hex)},
	}, s3.WithPresignExpires(videoURLTTL))
	if err != nil {
		return contracts.SignedRequest{}, fmt.Errorf("presign R2 video upload: %w", err)
	}
	return contracts.SignedRequest{
		URL:       result.URL,
		Headers:   uploadHeaders(result.SignedHeader, contentType),
		ExpiresAt: time.Now().UTC().Add(videoURLTTL),
	}, nil
}

// PresignVideoDownload firma un GET de 15 minutos. La URL nunca se registra.
func (c *Client) PresignVideoDownload(ctx context.Context, objectKey string) (contracts.SignedRequest, error) {
	if err := validateVideoKey(objectKey); err != nil {
		return contracts.SignedRequest{}, err
	}
	result, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(videoURLTTL))
	if err != nil {
		return contracts.SignedRequest{}, fmt.Errorf("presign R2 video download: %w", err)
	}
	return contracts.SignedRequest{
		URL:       result.URL,
		Headers:   map[string][]string{},
		ExpiresAt: time.Now().UTC().Add(videoURLTTL),
	}, nil
}

// InspectVideo lee el objeto una sola vez en streaming: conserva solo la cabecera
// y alimenta el SHA-256, de modo que la memoria usada no depende del tamaño del video.
// La lectura queda anclada al ETag del HEAD y limitada por tamaño máximo y tiempo.
func (c *Client) InspectVideo(ctx context.Context, objectKey string) (contracts.VideoInspection, error) {
	if err := validateVideoKey(objectKey); err != nil {
		return contracts.VideoInspection{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, videoInspectTimeout)
	defer cancel()
	select {
	case c.videoInspection <- struct{}{}:
		defer func() { <-c.videoInspection }()
	case <-ctx.Done():
		return contracts.VideoInspection{}, ctx.Err()
	}
	head, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(objectKey)})
	if err != nil {
		if missingObject(err) {
			return contracts.VideoInspection{}, contracts.ErrObjectNotFound
		}
		return contracts.VideoInspection{}, fmt.Errorf("inspect private video header: %w", err)
	}
	size := aws.ToInt64(head.ContentLength)
	contentType := strings.ToLower(aws.ToString(head.ContentType))
	if _, allowed := allowedVideoMIMEs[contentType]; !allowed || size < 1 || size > c.videoMaxBytes {
		return contracts.VideoInspection{}, ErrInvalidMedia
	}
	object, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(objectKey), IfMatch: head.ETag})
	if err != nil {
		if missingObject(err) {
			return contracts.VideoInspection{}, contracts.ErrObjectNotFound
		}
		return contracts.VideoInspection{}, fmt.Errorf("inspect private video body: %w", err)
	}
	defer object.Body.Close()

	header := make([]byte, contracts.VideoHeaderBytes)
	n, err := io.ReadFull(object.Body, header)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return contracts.VideoInspection{}, fmt.Errorf("read private video header: %w", err)
	}
	header = header[:n]
	hasher := sha256.New()
	hasher.Write(header)
	// El límite de +1 detecta objetos que crecen entre HEAD y GET sin leer sin tope.
	rest, err := io.Copy(hasher, io.LimitReader(object.Body, c.videoMaxBytes-int64(n)+1))
	if err != nil {
		return contracts.VideoInspection{}, fmt.Errorf("hash private video: %w", err)
	}
	total := int64(n) + rest
	if total != size || total > c.videoMaxBytes {
		return contracts.VideoInspection{}, ErrInvalidMedia
	}
	return contracts.VideoInspection{
		ContentType:   contentType,
		ContentLength: total,
		SHA256Hex:     hex.EncodeToString(hasher.Sum(nil)),
		Header:        header,
	}, nil
}

var _ contracts.VideoStorage = (*Client)(nil)
