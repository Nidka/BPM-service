// Package domain contiene las reglas puras de los correos transaccionales de BPM: plantillas en español y la
// reescritura de los enlaces de acción de Firebase hacia la página propia de la app.
package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Message es un correo ya renderizado: asunto, HTML con estilos en línea y alternativa en texto plano.
type Message struct {
	Subject string
	HTML    string
	Text    string
}

// Modos de acción que atiende la página /auth/accion de la app.
const (
	ModeVerifyEmail   = "verifyEmail"
	ModeResetPassword = "resetPassword"
)

// ActionPath es la ruta de la app que resuelve los enlaces de correo.
const ActionPath = "/auth/accion"

// ErrInvalidActionLink indica que el enlace generado por Firebase no trae el modo o el código esperados.
var ErrInvalidActionLink = errors.New("enlace de acción inválido")

// Misma forma que valida la app (EmailActionLink.codePattern): el código nunca se interpreta, solo se reenvía.
var oobCodePattern = regexp.MustCompile(`^[A-Za-z0-9_=.-]{1,512}$`)

// ActionLink convierte el enlace que genera Firebase Admin (https://<proyecto>.firebaseapp.com/__/auth/action?mode=…&oobCode=…)
// en el enlace propio https://<app>/auth/accion?mode=…&oobCode=…&lang=es. Solo se conservan mode y oobCode; el resto de
// parámetros (apiKey, continueUrl, lang de Firebase) se descarta. wantMode es el modo que se pidió a Firebase.
func ActionLink(generated, appBase, wantMode string) (string, error) {
	source, err := url.Parse(generated)
	if err != nil {
		return "", ErrInvalidActionLink
	}
	query := source.Query()
	mode, code := query.Get("mode"), query.Get("oobCode")
	if mode != wantMode || !oobCodePattern.MatchString(code) {
		return "", ErrInvalidActionLink
	}
	base, err := url.Parse(strings.TrimRight(appBase, "/"))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return "", ErrInvalidActionLink
	}
	base.Path = ActionPath
	// Orden fijo y codificación estándar: facilita probar y evita parámetros inesperados.
	values := url.Values{}
	values.Set("mode", mode)
	values.Set("oobCode", code)
	values.Set("lang", "es")
	base.RawQuery = values.Encode()
	return base.String(), nil
}
