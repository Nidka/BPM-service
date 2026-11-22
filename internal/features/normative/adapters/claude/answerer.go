// Package claude responde consultas normativas con la API de Claude (Anthropic). La normativa activa
// va como documentos de texto con citas habilitadas, de modo que cada afirmación queda respaldada por un
// fragmento literal; el prefijo (instrucciones + documentos) se guarda en caché entre consultas.
package claude

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/Nidka/BPM-service/internal/features/normative/domain"
)

// system fija el comportamiento; es estable para que la caché de prefijo funcione.
const system = `Eres el asistente de normativa sanitaria de BPM, una app para restaurantes, cafetines y servicios de alimentos del Perú.
Respondes dudas sobre Buenas Prácticas de Manufactura usando EXCLUSIVAMENTE los documentos de normativa adjuntos.
Reglas:
- Responde en español claro, sin jerga, en pocas frases o una lista corta de pasos.
- Cada afirmación debe apoyarse en un fragmento de los documentos (usa las citas).
- Si los documentos no responden la pregunta, dilo en una frase y sugiere consultar a la autoridad sanitaria o al responsable de calidad; no completes con conocimiento propio.
- No inventes números, temperaturas, plazos ni artículos que no estén en los documentos.
- Si la pregunta no trata de normativa o inocuidad alimentaria, indica amablemente que solo respondes sobre la normativa cargada.`

// Answerer implementa application.Answerer con el SDK oficial de Anthropic.
type Answerer struct {
	client anthropic.Client
	model  string
}

// New crea el cliente; la clave nunca sale del servidor.
func New(apiKey, model string) *Answerer {
	return &Answerer{client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithMaxRetries(2)), model: model}
}

// Answer consulta al modelo con [documents] como única fuente.
func (a *Answerer) Answer(ctx context.Context, documents []domain.Document, question string) (domain.Answer, error) {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(documents)+1)
	for i, d := range documents {
		block := anthropic.NewBetaDocumentBlock(anthropic.BetaPlainTextSourceParam{Data: d.Content})
		block.OfDocument.Title = anthropic.String(d.Title)
		if d.Source != "" {
			block.OfDocument.Context = anthropic.String("Referencia: " + d.Source)
		}
		block.OfDocument.Citations = anthropic.BetaCitationsConfigParam{Enabled: anthropic.Bool(true)}
		// La normativa cambia poco: el último documento cierra el prefijo en caché.
		if i == len(documents)-1 {
			block.OfDocument.CacheControl = anthropic.NewBetaCacheControlEphemeralParam()
		}
		blocks = append(blocks, block)
	}
	blocks = append(blocks, anthropic.NewBetaTextBlock("Pregunta: "+question))

	resp, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     a.model,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(blocks...)},
		// Si el modelo rechaza por política, otro modelo responde dentro de la misma llamada.
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) || errors.Is(err, context.DeadlineExceeded) {
			return domain.Answer{}, domain.ErrUnavailable
		}
		return domain.Answer{}, err
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return domain.Answer{Text: "No puedo responder esta consulta. Reformúlala sobre la normativa cargada.", Declined: true, Citations: []domain.Citation{}}, nil
	}
	return toAnswer(resp.Content, documents), nil
}

// toAnswer arma el texto y las citas; sin citas la respuesta no está respaldada por la normativa.
func toAnswer(content []anthropic.BetaContentBlockUnion, documents []domain.Document) domain.Answer {
	var text strings.Builder
	citations := []domain.Citation{}
	seen := map[string]bool{}
	for _, block := range content {
		tb, ok := block.AsAny().(anthropic.BetaTextBlock)
		if !ok {
			continue
		}
		text.WriteString(tb.Text)
		for _, c := range tb.Citations {
			loc, ok := c.AsAny().(anthropic.BetaCitationCharLocation)
			if !ok || loc.DocumentIndex < 0 || int(loc.DocumentIndex) >= len(documents) {
				continue
			}
			doc := documents[loc.DocumentIndex]
			key := doc.ID + "\x00" + loc.CitedText
			if seen[key] {
				continue
			}
			seen[key] = true
			citations = append(citations, domain.Citation{DocumentID: doc.ID, DocumentTitle: doc.Title, CitedText: strings.TrimSpace(loc.CitedText)})
		}
	}
	return domain.Answer{Text: strings.TrimSpace(text.String()), Citations: citations, Declined: len(citations) == 0}
}
