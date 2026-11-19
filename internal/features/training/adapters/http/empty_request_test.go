package traininghttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Los clientes KMP y el proxy web envían `{}` en las acciones sin datos
// (ver video, publicar, confirmar); el backend debe aceptarlo igual que un cuerpo vacío.
func TestEmptyRequestAcceptsEmptyBodyOrEmptyObject(t *testing.T) {
	for _, body := range []string{"", "   ", "{}", " { } ", "{\n}"} {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		w := httptest.NewRecorder()
		if !emptyRequest(w, r) {
			t.Fatalf("cuerpo %q debió aceptarse; respuesta %d", body, w.Code)
		}
	}
}

func TestEmptyRequestRejectsDataQueryAndLargeBodies(t *testing.T) {
	cases := []struct{ target, body string }{
		{"/x", `{"a":1}`},
		{"/x", `[]`},
		{"/x", `null`},
		{"/x", "{" + strings.Repeat(" ", 100) + "}"},
		{"/x?a=1", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, c.target, strings.NewReader(c.body))
		w := httptest.NewRecorder()
		if emptyRequest(w, r) {
			t.Fatalf("%s con cuerpo %q debió rechazarse", c.target, c.body)
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s con cuerpo %q: esperaba 400, obtuvo %d", c.target, c.body, w.Code)
		}
	}
}
