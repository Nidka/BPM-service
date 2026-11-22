package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFragmentsSplitByParagraphAndLongTextBySentence(t *testing.T) {
	got := Fragments("Artículo 1\nLos alimentos\nrefrigerados.\n\n\n  \nArtículo 2. Congelados.")
	if len(got) != 2 || got[0] != "Artículo 1 Los alimentos refrigerados." || got[1] != "Artículo 2. Congelados." {
		t.Fatalf("párrafos: %q", got)
	}
	long := strings.Repeat("Conservar los alimentos refrigerados entre 0 °C y 4 °C. ", 80)
	parts := Fragments(long)
	if len(parts) < 3 {
		t.Fatalf("debía partir el párrafo largo: %d", len(parts))
	}
	for _, p := range parts {
		if n := utf8.RuneCountInString(p); n == 0 || n > MaxFragmentLength || !utf8.ValidString(p) {
			t.Fatalf("fragmento inválido de %d caracteres", n)
		}
		if !strings.HasSuffix(p, ".") {
			t.Fatalf("debía cortar al final de una oración: %q", p[len(p)-20:])
		}
	}
	if strings.Join(parts, " ") != strings.TrimSpace(strings.Join(strings.Fields(long), " ")) {
		t.Fatal("no se debe perder texto al partir")
	}
	if got := Fragments(strings.Repeat("ñ", 4000)); len(got) != 3 || !utf8.ValidString(got[0]) {
		t.Fatalf("sin espacios también se parte sin romper caracteres: %d", len(got))
	}
}

func TestNormalizeQueryAndDocument(t *testing.T) {
	if q, err := NormalizeQuery("  cadena   de frío "); err != nil || q != "cadena de frío" {
		t.Fatalf("q=%q err=%v", q, err)
	}
	for _, bad := range []string{"", "a", strings.Repeat("x", 201)} {
		if _, err := NormalizeQuery(bad); err == nil {
			t.Errorf("%q debía rechazarse", bad)
		}
	}
	if _, err := NormalizeDocument(DocumentInput{}); err == nil {
		t.Fatal("debía exigir título y texto")
	}
	got, err := NormalizeDocument(DocumentInput{Title: " NTS 142 ", Content: "\ufeffa\r\nb"})
	if err != nil || got.Title != "NTS 142" || got.Content != "a\nb" {
		t.Fatalf("normalización: %+v %v", got, err)
	}
}
