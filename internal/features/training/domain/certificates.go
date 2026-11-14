package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Estados públicos de una constancia.
const (
	CertificateValid   = "VALID"
	CertificateRevoked = "REVOKED"
)

var verificationCodePattern = regexp.MustCompile(`^[A-Z2-7]{24}$`)

// ValidVerificationCode comprueba el formato del código (24 caracteres base32).
func ValidVerificationCode(code string) bool { return verificationCodePattern.MatchString(code) }

// PublicCertificate son los únicos datos que expone la verificación pública.
type PublicCertificate struct {
	CourseTitle   string
	VersionNumber int
	IssuedAt      time.Time
	Organization  string
	Status        string
	Holder        string
}

// AbbreviateHolder reduce un nombre completo a "Nombre A." (primer nombre y la
// inicial del primer apellido) para no exponer el nombre completo.
func AbbreviateHolder(display string) string {
	words := strings.Fields(display)
	if len(words) == 0 {
		return ""
	}
	first := []rune(words[0])
	if len(words) == 1 {
		return string(unicode.ToUpper(first[0])) + "."
	}
	return string(first) + " " + string(unicode.ToUpper([]rune(words[1])[0])) + "."
}
