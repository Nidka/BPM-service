package domain

import "strings"

// Categorías integradas: cualquier persona que pueda reportar puede elegirlas aunque la organización no haya
// configurado un catálogo. La primera vez que se usan, el servidor crea la fila del catálogo de la organización
// (versión 1) con estos valores; desde entonces son categorías normales (versionables por la administración).
// El código es estable y es lo que identifica la categoría integrada en el reporte (`catalogCode`).
var defaultCategories = []Catalog{
	{Code: "TEMPERATURA", Name: "Temperatura", Area: "Cocina y almacén", RiskLevel: "HIGH",
		NormativeCriterion: "Los alimentos se conservan a la temperatura adecuada; una desviación se reporta para corregirla."},
	{Code: "LIMPIEZA", Name: "Limpieza", Area: "General", RiskLevel: "MEDIUM",
		NormativeCriterion: "Las superficies, utensilios y áreas se mantienen limpios y desinfectados."},
	{Code: "PLAGAS", Name: "Plagas", Area: "General", RiskLevel: "HIGH",
		NormativeCriterion: "El establecimiento se mantiene libre de plagas; cualquier indicio se reporta de inmediato."},
	{Code: "EQUIPO_DANADO", Name: "Equipo dañado", Area: "Equipos", RiskLevel: "MEDIUM",
		NormativeCriterion: "Los equipos y utensilios funcionan y se mantienen en buen estado."},
	{Code: "PRODUCTO_MAL_ESTADO", Name: "Producto en mal estado", Area: "Almacén e insumos", RiskLevel: "HIGH",
		NormativeCriterion: "Los insumos y productos se usan dentro de su vigencia y en buen estado."},
	{Code: "HIGIENE_PERSONAL", Name: "Higiene del personal", Area: "Personal", RiskLevel: "MEDIUM",
		NormativeCriterion: "El personal cumple con la higiene y el lavado de manos."},
	{Code: "OTRO", Name: "Otro problema", Area: "General", RiskLevel: "MEDIUM",
		NormativeCriterion: "Reporte libre del personal; la administración lo clasifica al atenderlo."},
}

// DefaultCategory devuelve la categoría integrada con el [code] dado (sin distinguir mayúsculas).
func DefaultCategory(code string) (Catalog, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, c := range defaultCategories {
		if c.Code == code {
			return c, true
		}
	}
	return Catalog{}, false
}

// DefaultCategories devuelve una copia de las categorías integradas.
func DefaultCategories() []Catalog { return append([]Catalog(nil), defaultCategories...) }
