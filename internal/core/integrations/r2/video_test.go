package r2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
)

const trainingKey = "organizations/org/training/videos/10000000-0000-0000-0000-000000000001.mp4"

func videoClient(t *testing.T, endpoint string, max int64) *Client {
	t.Helper()
	client, err := New(context.Background(), config.R2Config{Endpoint: endpoint, Bucket: "private", AccessKeyID: "test-only", SecretKey: "test-only", SignedURLTTL: time.Minute, MaxUploadBytes: 1024, TrainingVideoMaxBytes: max, AllowedMIMETypes: map[string]struct{}{"image/png": {}}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestValidateVideoKeyAcceptsOnlyTrainingVideoLocation(t *testing.T) {
	if err := validateObjectKey(trainingKey); err != nil {
		t.Fatalf("la clave de capacitación debe cumplir organizations/...: %v", err)
	}
	if err := validateVideoKey(trainingKey); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"organizations/org/locations/site/media/a.mp4",
		"organizations/org/training/videos/../../other/a.mp4",
		"organizations/org/training/videos/sub/a.mp4",
		"organizations/org/training/videos/",
		"organizations//training/videos/a.mp4",
		"public/training/videos/a.mp4",
	} {
		if validateVideoKey(key) == nil {
			t.Fatalf("clave inválida aceptada: %s", key)
		}
	}
}

func TestVideoSignedUploadIsConditionalAndBounded(t *testing.T) {
	client := videoClient(t, "https://example.r2.test", 5<<20)
	hash := strings.Repeat("a", 64)
	signed, err := client.PresignVideoUpload(context.Background(), contracts.UploadRequest{ObjectKey: trainingKey, ContentType: "video/mp4", ContentLength: 4 << 20, SHA256Hex: hash})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signed.URL)
	if parsed.Query().Get("X-Amz-Expires") != "900" || http.Header(signed.Headers).Get("If-None-Match") != "*" {
		t.Fatal("la firma debe durar 15 minutos y prohibir sobrescritura")
	}
	if !strings.Contains(parsed.Query().Get("X-Amz-SignedHeaders"), "if-none-match") {
		t.Fatal("If-None-Match no está firmado")
	}
	for name, req := range map[string]contracts.UploadRequest{
		"mime":   {ObjectKey: trainingKey, ContentType: "image/png", ContentLength: 10, SHA256Hex: hash},
		"mov":    {ObjectKey: trainingKey, ContentType: "video/quicktime", ContentLength: 10, SHA256Hex: hash},
		"grande": {ObjectKey: trainingKey, ContentType: "video/mp4", ContentLength: 5<<20 + 1, SHA256Hex: hash},
		"vacío":  {ObjectKey: trainingKey, ContentType: "video/mp4", ContentLength: 0, SHA256Hex: hash},
		"hash":   {ObjectKey: trainingKey, ContentType: "video/mp4", ContentLength: 10, SHA256Hex: "zz"},
		"clave":  {ObjectKey: "organizations/org/other/a.mp4", ContentType: "video/mp4", ContentLength: 10, SHA256Hex: hash},
	} {
		if _, err := client.PresignVideoUpload(context.Background(), req); err == nil {
			t.Fatalf("aceptó solicitud inválida: %s", name)
		}
	}
	download, err := client.PresignVideoDownload(context.Background(), trainingKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = url.Parse(download.URL)
	if parsed.Query().Get("X-Amz-Expires") != "900" || !strings.Contains(parsed.Path, "training/videos") {
		t.Fatal("GET firmado de video inválido")
	}
}

func TestInspectVideoHashesWholeObjectInStreaming(t *testing.T) {
	body := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte{7}, 100_000)...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("ETag", `"video"`)
		if r.Method == http.MethodGet {
			w.Write(body)
		}
	}))
	defer server.Close()
	client := videoClient(t, server.URL, 1<<20)
	got, err := client.InspectVideo(context.Background(), trainingKey)
	sum := sha256.Sum256(body)
	if err != nil || got.SHA256Hex != hex.EncodeToString(sum[:]) || got.ContentLength != int64(len(body)) || got.ContentType != "video/mp4" {
		t.Fatal("inspección incorrecta", err, got)
	}
	if string(got.Header[4:8]) != "ftyp" || len(got.Header) != contracts.VideoHeaderBytes {
		t.Fatal("cabecera no devuelta", got.Header)
	}
}

func TestInspectVideoRejectsOversizeWrongTypeAndMapsMissing(t *testing.T) {
	mode := "grande"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "ausente":
			w.WriteHeader(404)
			return
		case "tipo":
			w.Header().Set("Content-Type", "text/html")
		default:
			w.Header().Set("Content-Type", "video/webm")
		}
		w.Header().Set("Content-Length", "2097152")
		if r.Method == http.MethodGet {
			w.Write(make([]byte, 2<<20))
		}
	}))
	defer server.Close()
	client := videoClient(t, server.URL, 1<<20)
	if _, err := client.InspectVideo(context.Background(), trainingKey); !errors.Is(err, ErrInvalidMedia) {
		t.Fatal("aceptó un video por encima del límite", err)
	}
	mode = "tipo"
	if _, err := client.InspectVideo(context.Background(), trainingKey); !errors.Is(err, ErrInvalidMedia) {
		t.Fatal("aceptó un tipo no permitido", err)
	}
	mode = "ausente"
	if _, err := client.InspectVideo(context.Background(), trainingKey); !errors.Is(err, contracts.ErrObjectNotFound) {
		t.Fatal("objeto ausente sin traducir", err)
	}
}
