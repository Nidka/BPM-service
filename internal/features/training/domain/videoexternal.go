package domain

import (
	"net/url"
	"regexp"
	"strings"
)

// ProviderYouTube es el único proveedor de video externo admitido.
const ProviderYouTube = "YOUTUBE"

var youtubeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ExternalVideo es un video alojado fuera de BPM. Solo se guarda el proveedor y el
// identificador; el cliente construye la URL de inserción con el dominio sin cookies.
type ExternalVideo struct{ Provider, VideoID string }

// ValidYouTubeID indica si v es un identificador de video de YouTube.
func ValidYouTubeID(v string) bool { return youtubeIDPattern.MatchString(v) }

// ParseYouTubeURL normaliza una URL de YouTube a su identificador de video. Admite
// youtube.com/watch?v=, youtu.be/, youtube.com/shorts/ y youtube-nocookie.com/embed/.
// Cualquier otro dominio, puerto, credencial o formato es inválido: nunca se
// conservan URL arbitrarias.
func ParseYouTubeURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 300 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	path := strings.Trim(u.EscapedPath(), "/")
	var id string
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		switch {
		case path == "watch":
			id = u.Query().Get("v")
		case strings.HasPrefix(path, "shorts/") && strings.Count(path, "/") == 1:
			id = strings.TrimPrefix(path, "shorts/")
		}
	case "youtu.be", "www.youtu.be":
		if !strings.Contains(path, "/") {
			id = path
		}
	case "youtube-nocookie.com", "www.youtube-nocookie.com":
		if strings.HasPrefix(path, "embed/") && strings.Count(path, "/") == 1 {
			id = strings.TrimPrefix(path, "embed/")
		}
	}
	if !ValidYouTubeID(id) {
		return "", false
	}
	return id, true
}

// NormalizeExternalVideo resuelve la entrada de autoría (identificador o URL) a un
// ExternalVideo canónico. ok=false indica que el valor no es de YouTube.
func NormalizeExternalVideo(provider, videoID, rawURL string) (*ExternalVideo, bool) {
	if provider != ProviderYouTube {
		return nil, false
	}
	switch {
	case videoID != "" && rawURL == "":
		if !ValidYouTubeID(videoID) {
			return nil, false
		}
	case rawURL != "" && videoID == "":
		id, ok := ParseYouTubeURL(rawURL)
		if !ok {
			return nil, false
		}
		videoID = id
	default:
		return nil, false
	}
	return &ExternalVideo{Provider: ProviderYouTube, VideoID: videoID}, true
}
