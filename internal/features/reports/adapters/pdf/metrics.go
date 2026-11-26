package pdf

import "strings"

// Anchos de glifo de Helvetica y Helvetica-Bold (milésimas de em) para los caracteres ASCII 32 a 126.
var (
	regularWidths = [95]int{
		278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, // ␠ ! " # $ % & ' ( ) * + , - . /
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556, // 0-9 : ; < = > ?
		1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778, // @ A-O
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556, // P-Z [ \ ] ^ _
		333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556, // ` a-o
		556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584, // p-z { | } ~
	}
	boldWidths = [95]int{
		278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278,
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611,
		975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778,
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556,
		333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611,
		611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584,
	}
)

// letrasBase asocia las letras acentuadas de Latin-1 con la letra sin acento: comparten ancho en Helvetica.
var letrasBase = map[rune]rune{}

func init() {
	for base, group := range map[rune]string{
		'A': "ÀÁÂÃÄÅ", 'C': "Ç", 'E': "ÈÉÊË", 'I': "ÌÍÎÏ", 'N': "Ñ", 'O': "ÒÓÔÕÖØ", 'U': "ÙÚÛÜ", 'Y': "Ý",
		'a': "àáâãäå", 'c': "ç", 'e': "èéêë", 'i': "ìíîï", 'n': "ñ", 'o': "òóôõöø", 'u': "ùúûü", 'y': "ýÿ",
	} {
		for _, r := range group {
			letrasBase[r] = base
		}
	}
}

// glyphWidth es el ancho aproximado de r. Fuera de ASCII usa la letra base de las acentuadas y valores fijos para
// los signos frecuentes del español; el resto cuenta como un dígito. Solo sirve para ajustar columnas.
func glyphWidth(f Font, r rune) int {
	widths := &regularWidths
	if f == Bold {
		widths = &boldWidths
	}
	switch {
	case r >= 32 && r <= 126:
		return widths[r-32]
	case letrasBase[r] != 0:
		return widths[letrasBase[r]-32]
	}
	switch r {
	case '…':
		return 1000
	case '·':
		return 278
	case '¡':
		return 333
	case '¿':
		return 611
	case '°':
		return 400
	case '–':
		return 556
	case '—':
		return 1000
	case '•':
		return 350
	}
	if strings.ContainsRune("\t\n\r", r) {
		return widths[0]
	}
	return 556
}
