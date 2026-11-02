package r2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateObjectKey(t *testing.T) {
	valid := "organizations/tenant/locations/location/media/file.jpg"
	if err := validateObjectKey(valid); err != nil {
		t.Fatalf("expected valid object key: %v", err)
	}

	invalid := []string{
		"public/file.jpg",
		"/organizations/tenant/file.jpg",
		"organizations/tenant/../other/file.jpg",
		"organizations\\tenant\\file.jpg",
	}
	for _, key := range invalid {
		if err := validateObjectKey(key); err == nil {
			t.Fatalf("expected invalid object key: %s", key)
		}
	}
}

func TestSignedUploadCannotOverwriteAndInspectionHashesActualBytes(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	body := encoded.Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("ETag", `"immutable-object"`)
		w.Header().Set("X-Amz-Meta-Sha256", strings.Repeat("0", 64))
		if r.Method == http.MethodGet {
			w.Write(body)
		}
	}))
	defer server.Close()
	client, err := New(context.Background(), config.R2Config{Endpoint: server.URL, Bucket: "private", AccessKeyID: "test-only-key", SecretKey: "test-only-secret", SignedURLTTL: 5 * time.Minute, MaxUploadBytes: 1024 * 1024, AllowedMIMETypes: map[string]struct{}{"image/png": {}}})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := client.PresignUpload(context.Background(), contracts.UploadRequest{ObjectKey: "organizations/org/locations/site/media/file.png", ContentType: "image/png", ContentLength: int64(len(body)), SHA256Hex: strings.Repeat("0", 64)})
	if err != nil {
		t.Fatal(err)
	}
	url, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url.Query().Get("X-Amz-SignedHeaders"), "if-none-match") || http.Header(signed.Headers).Get("If-None-Match") != "*" {
		t.Fatal("la firma permite reemplazar el objeto")
	}
	got, err := client.Inspect(context.Background(), "organizations/org/locations/site/media/file.png")
	sum := sha256.Sum256(body)
	if err != nil || got.SHA256Hex != hex.EncodeToString(sum[:]) || got.ContentType != "image/png" || got.ContentLength != int64(len(body)) {
		t.Fatal("inspección no verificada", err, got)
	}
}

func TestInspectionRejectsFakeImageAndMapsMissingObject(t *testing.T) {
	missing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if missing {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "17")
		if r.Method == http.MethodGet {
			w.Write([]byte("<html>fake</html>"))
		}
	}))
	defer server.Close()
	client, err := New(context.Background(), config.R2Config{Endpoint: server.URL, Bucket: "private", AccessKeyID: "test-only", SecretKey: "test-only", SignedURLTTL: time.Minute, MaxUploadBytes: 1024, AllowedMIMETypes: map[string]struct{}{"image/png": {}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Inspect(context.Background(), "organizations/org/file.png"); !errors.Is(err, ErrInvalidMedia) {
		t.Fatal("aceptó HTML como imagen", err)
	}
	missing = true
	if _, err = client.Inspect(context.Background(), "organizations/org/missing.png"); !errors.Is(err, contracts.ErrObjectNotFound) {
		t.Fatal("error de objeto ausente no traducido", err)
	}
}

func TestInspectionRejectsTruncatedImageWithValidHeader(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	body := encoded.Bytes()[:33]
	if _, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil {
		t.Fatal("fixture debe conservar cabecera válida", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("ETag", `"truncated"`)
		if r.Method == http.MethodGet {
			w.Write(body)
		}
	}))
	defer server.Close()
	client, err := New(context.Background(), config.R2Config{Endpoint: server.URL, Bucket: "private", AccessKeyID: "test", SecretKey: "test", SignedURLTTL: time.Minute, MaxUploadBytes: 1024, AllowedMIMETypes: map[string]struct{}{"image/png": {}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Inspect(context.Background(), "organizations/org/truncated.png"); !errors.Is(err, ErrInvalidMedia) {
		t.Fatal("aceptó una imagen incompleta", err)
	}
}
