package domain

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"
)

// Imágenes de la mascota que sirve la propia app (Compose Resources); se usan con el origen público como prefijo.
const (
	mascotReady = "/composeResources/pe.bpm.resources/drawable/mascot_ready.png"
	mascotFix   = "/composeResources/pe.bpm.resources/drawable/mascot_fix.png"
	mascotClass = "/composeResources/pe.bpm.resources/drawable/mascot_class.png"
)

// ActionData son los datos de los correos de verificación y de nueva contraseña.
type ActionData struct {
	// AppURL es el origen público de la app (sin barra final); de él salen la mascota y el pie.
	AppURL string
	// Link es el enlace ya reescrito hacia /auth/accion.
	Link string
}

// InvitationData son los datos del correo de invitación a una organización.
type InvitationData struct {
	AppURL           string
	Link             string
	OrganizationName string
	// Email es el correo invitado; la cuenta que acepta debe usar el mismo.
	Email string
	// ExpiresAt es la fecha de vencimiento (RFC 3339); si no se puede leer se muestra tal cual o se omite.
	ExpiresAt string
}

// VerifyEmail es el correo «Confirma tu correo».
func VerifyEmail(data ActionData) Message {
	return render(spec{
		subject:   "Confirma tu correo en BPM",
		preheader: "Un último paso para activar tu cuenta.",
		title:     "Confirma tu correo",
		paragraphs: []string{
			"¡Hola! Gracias por crear tu cuenta en BPM.",
			"Confirma que este correo es tuyo para empezar a usar la app.",
		},
		button:    "Verificar mi correo",
		link:      data.Link,
		note:      "Por seguridad, el enlace vence y solo puede usarse una vez. Si no creaste una cuenta en BPM, ignora este mensaje: no se hará ningún cambio.",
		mascot:    mascotReady,
		mascotAlt: "Tomate, la mascota de BPM, con su lista de control",
		appURL:    data.AppURL,
	})
}

// PasswordReset es el correo «Crea tu nueva contraseña».
func PasswordReset(data ActionData) Message {
	return render(spec{
		subject:   "Crea una nueva contraseña en BPM",
		preheader: "Elige una contraseña nueva para tu cuenta.",
		title:     "Crea tu nueva contraseña",
		paragraphs: []string{
			"Recibimos una solicitud para restablecer la contraseña de tu cuenta BPM.",
			"Pulsa el botón para elegir una contraseña nueva.",
		},
		button:    "Crear nueva contraseña",
		link:      data.Link,
		note:      "Por seguridad, el enlace vence pronto y solo puede usarse una vez. Si no fuiste tú, ignora este mensaje: tu contraseña actual sigue funcionando.",
		mascot:    mascotFix,
		mascotAlt: "Tomate, la mascota de BPM, con una lupa",
		appURL:    data.AppURL,
	})
}

// Invitation es el correo «Te invitaron a unirte a …».
func Invitation(data InvitationData) Message {
	org := strings.TrimSpace(data.OrganizationName)
	if org == "" {
		org = "una organización"
	}
	paragraphs := []string{
		"Te invitaron a unirte a " + org + " en BPM, la app de buenas prácticas de manipulación.",
		"Inicia sesión o crea tu cuenta con este mismo correo (" + data.Email + ") y acepta la invitación.",
	}
	note := "La invitación solo puede usarse una vez"
	if date := spanishDate(data.ExpiresAt); date != "" {
		note += " y vence el " + date
	}
	note += ". Si no la esperabas, ignora este mensaje."
	return render(spec{
		subject:    "Invitación a " + org + " en BPM",
		preheader:  "Acepta la invitación para unirte al equipo.",
		title:      "Te invitaron a unirte a " + org,
		paragraphs: paragraphs,
		button:     "Aceptar invitación",
		link:       data.Link,
		note:       note,
		mascot:     mascotClass,
		mascotAlt:  "Tomate, la mascota de BPM, leyendo",
		appURL:     data.AppURL,
	})
}

var months = [...]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// spanishDate formatea «2 de enero de 2026» en hora de Lima (UTC-5, sin horario de verano); "" si no hay fecha válida.
func spanishDate(rfc3339 string) string {
	if strings.TrimSpace(rfc3339) == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ""
	}
	t = t.In(time.FixedZone("America/Lima", -5*3600))
	return fmt.Sprintf("%d de %s de %d", t.Day(), months[t.Month()-1], t.Year())
}

type spec struct {
	subject, preheader, title string
	paragraphs                []string
	button, link, note        string
	mascot, mascotAlt, appURL string
}

// safeURL devuelve la URL solo si es http(s) absoluta; si no, "#" (nunca un esquema ejecutable).
func safeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "#"
	}
	return parsed.String()
}

func esc(text string) string { return html.EscapeString(text) }

// Tipografía: Baloo 2 y Nunito si el cliente las carga (Apple Mail, iOS); si no, familias del sistema de aspecto similar.
const (
	headFont = `'Baloo 2','Nunito','Trebuchet MS','Segoe UI',Arial,sans-serif`
	bodyFont = `'Nunito','Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif`
)

func render(s spec) Message {
	link := safeURL(s.link)
	app := strings.TrimRight(safeURL(s.appURL), "/")
	if app == "#" {
		app = ""
	}
	var h strings.Builder
	h.WriteString(`<!DOCTYPE html>
<html lang="es" xmlns="http://www.w3.org/1999/xhtml" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<meta name="x-apple-disable-message-reformatting">
<meta name="format-detection" content="telephone=no,address=no,email=no,date=no,url=no">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>` + esc(s.subject) + `</title>
<!--[if mso]><noscript><xml><o:OfficeDocumentSettings><o:PixelsPerInch>96</o:PixelsPerInch></o:OfficeDocumentSettings></xml></noscript><![endif]-->
<link href="https://fonts.googleapis.com/css2?family=Baloo+2:wght@600;700&family=Nunito:wght@400;600;700&display=swap" rel="stylesheet">
<style>
  :root{color-scheme:light dark;supported-color-schemes:light dark}
  body,table,td,a{-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%}
  table,td{mso-table-lspace:0;mso-table-rspace:0}
  img{-ms-interpolation-mode:bicubic;border:0;outline:none;text-decoration:none}
  a{color:#005B59}
  @media (max-width:620px){
    .wrap{width:100% !important}
    .pad{padding-left:22px !important;padding-right:22px !important}
    .h1{font-size:25px !important;line-height:30px !important}
    .btn a{display:block !important}
  }
  @media (prefers-color-scheme:dark){
    .bg-page{background-color:#0E1716 !important}
    .bg-card{background-color:#172321 !important}
    .t-title{color:#F1F7F5 !important}
    .t-body{color:#D2DDDA !important}
    .t-muted{color:#98ABA7 !important}
    .box{background-color:#20332F !important;border-color:#33504B !important}
    .btn-cell{background-color:#17807B !important}
    .lnk{color:#6FD1CB !important}
  }
  [data-ogsc] .bg-page{background-color:#0E1716 !important}
  [data-ogsc] .bg-card{background-color:#172321 !important}
  [data-ogsc] .t-title{color:#F1F7F5 !important}
  [data-ogsc] .t-body{color:#D2DDDA !important}
  [data-ogsc] .t-muted{color:#98ABA7 !important}
  [data-ogsc] .btn-cell{background-color:#17807B !important}
</style>
</head>
<body class="bg-page" style="margin:0;padding:0;width:100%;background-color:#F5F8F7;">
<div style="display:none;max-height:0;max-width:0;overflow:hidden;opacity:0;mso-hide:all;font-size:1px;line-height:1px;color:#F5F8F7;">` + esc(s.preheader) + strings.Repeat("&#847;&zwnj;&nbsp;", 30) + `</div>
<table role="presentation" class="bg-page" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#F5F8F7" style="background-color:#F5F8F7;">
<tr><td align="center" style="padding:24px 12px;">
<!--[if mso]><table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0"><tr><td><![endif]-->
<table role="presentation" class="wrap" width="600" cellpadding="0" cellspacing="0" border="0" style="width:600px;max-width:600px;">
<tr><td align="center" bgcolor="#005B59" style="background-color:#005B59;border-radius:20px 20px 0 0;padding:26px 24px 22px 24px;">
<div style="font-family:` + headFont + `;font-size:34px;line-height:36px;font-weight:700;letter-spacing:1px;color:#FFFFFF;">BPM</div>
<div style="font-family:` + bodyFont + `;font-size:13px;line-height:18px;font-weight:600;color:#CFE8E4;padding-top:4px;">Buenas Prácticas de Manipulación</div>
</td></tr>
<tr><td class="bg-card pad" bgcolor="#FFFFFF" style="background-color:#FFFFFF;padding:30px 36px 8px 36px;border-radius:0 0 20px 20px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
`)
	if app != "" && s.mascot != "" {
		h.WriteString(`<tr><td align="center" style="padding:0 0 18px 0;"><table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td align="center" bgcolor="#E3F1EE" style="background-color:#E3F1EE;border-radius:60px;padding:12px 18px;"><img src="` + esc(app+s.mascot) + `" width="110" height="110" alt="` + esc(s.mascotAlt) + `" style="display:block;width:110px;height:110px;max-width:110px;"></td></tr></table></td></tr>
`)
	}
	h.WriteString(`<tr><td align="center" class="t-title h1" style="font-family:` + headFont + `;font-size:28px;line-height:34px;font-weight:700;color:#0F2D2C;padding:0 0 14px 0;">` + esc(s.title) + `</td></tr>
`)
	for _, p := range s.paragraphs {
		h.WriteString(`<tr><td align="center" class="t-body" style="font-family:` + bodyFont + `;font-size:16px;line-height:25px;color:#33504D;padding:0 0 10px 0;">` + esc(p) + `</td></tr>
`)
	}
	h.WriteString(`<tr><td align="center" class="btn" style="padding:18px 0 22px 0;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
<td class="btn-cell" align="center" bgcolor="#005B59" style="background-color:#005B59;border-radius:14px;mso-padding-alt:16px 38px;">
<a href="` + esc(link) + `" target="_blank" style="display:inline-block;padding:16px 38px;font-family:` + headFont + `;font-size:18px;line-height:22px;font-weight:700;color:#FFFFFF;text-decoration:none;border-radius:14px;">` + esc(s.button) + `</a>
</td></tr></table>
</td></tr>
<tr><td class="box" style="background-color:#F1F6F5;border:1px solid #DCE8E5;border-radius:12px;padding:14px 16px;">
<div class="t-muted" style="font-family:` + bodyFont + `;font-size:13px;line-height:19px;color:#5B7370;">Si el botón no funciona, copia y pega este enlace en tu navegador:</div>
<div style="font-family:` + bodyFont + `;font-size:12px;line-height:18px;padding-top:6px;word-break:break-all;overflow-wrap:anywhere;"><a class="lnk" href="` + esc(link) + `" target="_blank" style="color:#005B59;text-decoration:underline;">` + esc(link) + `</a></div>
</td></tr>
<tr><td align="center" class="t-muted" style="font-family:` + bodyFont + `;font-size:13px;line-height:20px;color:#5B7370;padding:20px 0 24px 0;">` + esc(s.note) + `</td></tr>
</table>
</td></tr>
<tr><td align="center" class="t-muted" style="font-family:` + bodyFont + `;font-size:12px;line-height:18px;color:#6B817E;padding:18px 16px 4px 16px;">Este es un mensaje automático de BPM. Por favor, no compartas este correo ni su enlace con nadie.`)
	if app != "" {
		h.WriteString(`<br><a class="lnk" href="` + esc(app) + `" target="_blank" style="color:#005B59;text-decoration:underline;">` + esc(strings.TrimPrefix(strings.TrimPrefix(app, "https://"), "http://")) + `</a>`)
	}
	h.WriteString(`</td></tr>
</table>
<!--[if mso]></td></tr></table><![endif]-->
</td></tr>
</table>
</body>
</html>
`)

	var t strings.Builder
	t.WriteString(s.title + "\n\n")
	for _, p := range s.paragraphs {
		t.WriteString(p + "\n\n")
	}
	t.WriteString(s.button + ":\n" + link + "\n\n" + s.note + "\n\n-- \nBPM · Buenas Prácticas de Manipulación\nEste es un mensaje automático. No compartas este correo ni su enlace con nadie.\n")
	return Message{Subject: s.subject, HTML: h.String(), Text: t.String()}
}
