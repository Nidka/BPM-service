package domain

import "strings"

// NormalizeWhatsApp limpia un número de WhatsApp a solo dígitos con código de país (E.164 sin «+»). Un celular peruano
// de 9 dígitos que empieza en 9 recibe el 51. Vacío significa «sin número».
func NormalizeWhatsApp(raw string) (string, error) {
	digits := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(raw))
	digits = strings.TrimPrefix(digits, "+")
	if digits == "" {
		return "", nil
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", &ValidationError{Fields: map[string]string{"whatsapp": "Escribe solo números, por ejemplo 987654321."}}
		}
	}
	if len(digits) == 9 && digits[0] == '9' {
		digits = "51" + digits
	}
	if len(digits) < 8 || len(digits) > 15 || digits[0] == '0' {
		return "", &ValidationError{Fields: map[string]string{"whatsapp": "Escribe tu celular, por ejemplo 987654321, o con código de país."}}
	}
	return digits, nil
}
