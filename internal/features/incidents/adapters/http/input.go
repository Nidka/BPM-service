package incidentshttp

import (
	"bytes"
	"encoding/json"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"io"
	"mime"
	"net/http"
	"net/url"
	"unicode/utf8"
)

func invalidRequest(w http.ResponseWriter, r *http.Request) {
	response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
}

// decode rechaza UTF8 inválido, propiedades repetidas, null y campos desconocidos.
func decode[T any](w http.ResponseWriter, r *http.Request) (v T, ok bool) {
	kind, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || kind != "application/json" || r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10))
	if e != nil || !utf8.Valid(b) {
		invalidRequest(w, r)
		return
	}
	d := json.NewDecoder(bytes.NewReader(b))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		invalidRequest(w, r)
		return
	}
	seen := map[string]bool{}
	for d.More() {
		token, e = d.Token()
		if e != nil {
			invalidRequest(w, r)
			return
		}
		key, yes := token.(string)
		if !yes || seen[key] {
			invalidRequest(w, r)
			return
		}
		seen[key] = true
		var raw json.RawMessage
		if e = d.Decode(&raw); e != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			invalidRequest(w, r)
			return
		}
	}
	if _, e = d.Token(); e != nil {
		invalidRequest(w, r)
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		invalidRequest(w, r)
		return
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&v); e != nil {
		invalidRequest(w, r)
		return
	}
	return v, true
}
func (h *Handler) query(w http.ResponseWriter, r *http.Request, site, history bool) (location, status string, p pagination.Request, ok bool) {
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	if site {
		values := q["locationId"]
		if len(values) != 1 || !domain.ValidID(values[0]) {
			h.fail(w, r, pagination.ErrInvalid)
			return
		}
		location = values[0]
		q.Del("locationId")
		if values, present := q["status"]; present {
			if len(values) != 1 || !domain.ValidStatus(values[0]) {
				h.fail(w, r, pagination.ErrInvalid)
				return
			}
			status = values[0]
			q.Del("status")
		}
	}
	if history {
		p, e = request.Pagination(q.Encode(), "pageSize", "pageToken")
	} else {
		p, e = request.Pagination(q.Encode())
	}
	if e != nil {
		h.fail(w, r, e)
		return
	}
	if r.Body != nil {
		b, e := io.ReadAll(io.LimitReader(r.Body, 1))
		if e != nil || len(b) > 0 {
			invalidRequest(w, r)
			return
		}
	}
	return location, status, p, true
}
