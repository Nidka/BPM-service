package request

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeStrictJSONRejectsAmbiguousDocuments(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"active":true,"active":false}`, `{"configuration":{"x":1,"x":2}}`, `{"extra":1}`, "{\"active\":\"\xff\"}", `{"active":true} {}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		var v struct {
			Active        *bool          `json:"active"`
			Configuration map[string]any `json:"configuration"`
		}
		if DecodeStrictJSON(w, r, &v, 512<<10) {
			t.Fatal("accepted", raw)
		}
	}
}
