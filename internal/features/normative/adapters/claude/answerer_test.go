package claude

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/Nidka/BPM-service/internal/features/normative/domain"
)

func TestToAnswerMapsCitationsToDocumentsAndMarksUnsupported(t *testing.T) {
	docs := []domain.Document{{ID: "a", Title: "NTS 142"}, {ID: "b", Title: "RM 822"}}
	var msg anthropic.BetaMessage
	raw := `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},
	"content":[{"type":"text","text":"Refrigera entre 0 y 4 °C.","citations":[
	  {"type":"char_location","cited_text":"de 0 °C a 4 °C","document_index":1,"document_title":"RM 822","start_char_index":0,"end_char_index":10},
	  {"type":"char_location","cited_text":"de 0 °C a 4 °C","document_index":1,"document_title":"RM 822","start_char_index":0,"end_char_index":10},
	  {"type":"char_location","cited_text":"fuera de rango","document_index":9,"document_title":"x","start_char_index":0,"end_char_index":3}]}]}`
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	got := toAnswer(msg.Content, docs)
	if got.Declined || len(got.Citations) != 1 || got.Citations[0].DocumentID != "b" || got.Citations[0].DocumentTitle != "RM 822" {
		t.Fatalf("citas mal asociadas: %+v", got)
	}
	var plain anthropic.BetaMessage
	_ = json.Unmarshal([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"La normativa cargada no trata ese tema."}]}`), &plain)
	if !toAnswer(plain.Content, docs).Declined {
		t.Fatal("sin citas la respuesta no está respaldada")
	}
}
