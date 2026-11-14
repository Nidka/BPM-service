package domain

import "time"

// Version resume una versión inmutable de una plantilla. Una ejecución conserva la versión con
// la que se inició; publicar otra versión nunca modifica sus preguntas ni sus respuestas.
type Version struct {
	ID, TemplateID, Status, ChangeNote string
	Number, ItemCount                  int
	PublishedAt                        *time.Time
}

// NewVersion describe la edición de una plantilla: base es la versión sobre la que se editó.
type NewVersion struct {
	VersionID, ChangeNote string
	BaseVersionNumber     int
	Items                 []Item
}
