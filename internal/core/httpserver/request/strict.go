package request

import (
	"bytes"
	"encoding/json"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"
)

// DecodeStrictJSON rechaza duplicados anidados, UTF8 inválido, campos desconocidos y documentos múltiples.
func DecodeStrictJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) bool {
	invalid := func() bool {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
		return false
	}
	kind, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || kind != "application/json" || r.URL.RawQuery != "" {
		return invalid()
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if e != nil || !utf8.Valid(raw) {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(bool) bool
	walk = func(root bool) bool {
		tok, e := d.Token()
		if e != nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return !root
		}
		if delim == '{' {
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return false
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return false
				}
				seen[key] = true
				if !walk(false) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim('}')
		}
		if delim == '[' && !root {
			for d.More() {
				if !walk(false) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim(']')
		}
		return false
	}
	if !walk(true) {
		return invalid()
	}
	if _, e = d.Token(); e != io.EOF {
		return invalid()
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(target); e != nil {
		return invalid()
	}
	return true
}
