package r2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

var (
	ErrInvalidObjectKey = errors.New("invalid private object key")
	ErrInvalidMedia     = contracts.ErrInvalidObject
)

type Client struct {
	bucket       string
	presign      *s3.PresignClient
	s3           *s3.Client
	ttl          time.Duration
	maxBytes     int64
	allowedMIMEs map[string]struct{}
	inspections  chan struct{}
}

func New(ctx context.Context, cfg config.R2Config) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize R2 configuration: %w", err)
	}

	awsCfg.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	awsCfg.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired

	s3Client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.Endpoint)
		options.UsePathStyle = true
	})

	return &Client{
		bucket:       cfg.Bucket,
		presign:      s3.NewPresignClient(s3Client),
		s3:           s3Client,
		ttl:          cfg.SignedURLTTL,
		maxBytes:     cfg.MaxUploadBytes,
		allowedMIMEs: cfg.AllowedMIMETypes,
		inspections:  make(chan struct{}, 2),
	}, nil
}

func (c *Client) PresignUpload(ctx context.Context, request contracts.UploadRequest) (contracts.SignedRequest, error) {
	if err := validateObjectKey(request.ObjectKey); err != nil {
		return contracts.SignedRequest{}, err
	}
	contentType := strings.ToLower(strings.TrimSpace(request.ContentType))
	if _, allowed := c.allowedMIMEs[contentType]; !allowed || request.ContentLength < 1 || request.ContentLength > c.maxBytes {
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
		ContentDisposition: aws.String("attachment"),
		Metadata:           map[string]string{"sha256": strings.ToLower(request.SHA256Hex)},
	}, s3.WithPresignExpires(c.ttl))
	if err != nil {
		return contracts.SignedRequest{}, fmt.Errorf("presign R2 upload: %w", err)
	}
	return contracts.SignedRequest{
		URL:       result.URL,
		Headers:   uploadHeaders(result.SignedHeader, contentType),
		ExpiresAt: time.Now().UTC().Add(c.ttl),
	}, nil
}

func (c *Client) PresignDownload(ctx context.Context, objectKey string) (contracts.SignedRequest, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return contracts.SignedRequest{}, err
	}
	result, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(c.ttl))
	if err != nil {
		return contracts.SignedRequest{}, fmt.Errorf("presign R2 download: %w", err)
	}
	return contracts.SignedRequest{
		URL:       result.URL,
		Headers:   map[string][]string{},
		ExpiresAt: time.Now().UTC().Add(c.ttl),
	}, nil
}

func (c *Client) Head(ctx context.Context, objectKey string) (contracts.ObjectMetadata, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return contracts.ObjectMetadata{}, err
	}
	result, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		if missingObject(err) {
			return contracts.ObjectMetadata{}, contracts.ErrObjectNotFound
		}
		return contracts.ObjectMetadata{}, fmt.Errorf("read R2 object metadata: %w", err)
	}
	return contracts.ObjectMetadata{
		ContentType:   aws.ToString(result.ContentType),
		ContentLength: aws.ToInt64(result.ContentLength),
		SHA256Hex:     result.Metadata["sha256"],
	}, nil
}

// Inspect no confía en metadata aportada por quien cargó el archivo. La lectura
// está acotada y el PUT firmado no permite sustituir un objeto ya existente.
func (c *Client) Inspect(ctx context.Context, objectKey string) (contracts.ObjectMetadata, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return contracts.ObjectMetadata{}, err
	}
	// Acota la memoria de la decodificación de imágenes ante cargas simultáneas.
	select {
	case c.inspections <- struct{}{}:
		defer func() { <-c.inspections }()
	case <-ctx.Done():
		return contracts.ObjectMetadata{}, ctx.Err()
	}
	head, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(objectKey)})
	if err != nil {
		if missingObject(err) {
			return contracts.ObjectMetadata{}, contracts.ErrObjectNotFound
		}
		return contracts.ObjectMetadata{}, fmt.Errorf("inspect private object header: %w", err)
	}
	if aws.ToInt64(head.ContentLength) < 1 || aws.ToInt64(head.ContentLength) > c.maxBytes {
		return contracts.ObjectMetadata{}, ErrInvalidMedia
	}
	object, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(objectKey), IfMatch: head.ETag})
	if err != nil {
		if missingObject(err) {
			return contracts.ObjectMetadata{}, contracts.ErrObjectNotFound
		}
		return contracts.ObjectMetadata{}, fmt.Errorf("inspect private object body: %w", err)
	}
	defer object.Body.Close()
	data, err := io.ReadAll(io.LimitReader(object.Body, c.maxBytes+1))
	if err != nil {
		return contracts.ObjectMetadata{}, fmt.Errorf("read bounded private object: %w", err)
	}
	if len(data) == 0 || int64(len(data)) > c.maxBytes || int64(len(data)) != aws.ToInt64(head.ContentLength) {
		return contracts.ObjectMetadata{}, ErrInvalidMedia
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" || mime != strings.ToLower(aws.ToString(head.ContentType)) {
		return contracts.ObjectMetadata{}, ErrInvalidMedia
	}
	dimensions, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || dimensions.Width < 1 || dimensions.Height < 1 || dimensions.Width > 8000 || dimensions.Height > 8000 || int64(dimensions.Width)*int64(dimensions.Height) > 16_000_000 {
		return contracts.ObjectMetadata{}, ErrInvalidMedia
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return contracts.ObjectMetadata{}, ErrInvalidMedia
	}
	sum := sha256.Sum256(data)
	return contracts.ObjectMetadata{ContentType: mime, ContentLength: int64(len(data)), SHA256Hex: hex.EncodeToString(sum[:])}, nil
}

func uploadHeaders(headers http.Header, contentType string) map[string][]string {
	result := headers.Clone()
	// El SDK no siempre incluye Content-Type en SignedHeader; el cliente debe enviarlo.
	result.Set("Content-Type", contentType)
	return result
}

func missingObject(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "404")
}

func validateObjectKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.ContainsRune(key, '\x00') {
		return ErrInvalidObjectKey
	}
	if path.Clean(key) != key || !strings.HasPrefix(key, "organizations/") {
		return ErrInvalidObjectKey
	}
	return nil
}

var _ contracts.ObjectStorage = (*Client)(nil)
