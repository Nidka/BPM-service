package domain

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/shared/domain/input"
)

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var quantityPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,8})(?:\.[0-9]{1,3})?$`)

func ValidID(id string) bool { return idPattern.MatchString(id) }

// Quantity conserva el decimal exacto que admite NUMERIC(12,3), sin float.
func Quantity(value string) (string, error) {
	if !quantityPattern.MatchString(value) {
		return "", ErrConflict
	}
	whole, fraction, _ := strings.Cut(value, ".")
	fraction = strings.TrimRight(fraction, "0")
	if whole == "0" && fraction == "" {
		return "", ErrConflict
	}
	if fraction == "" {
		return whole, nil
	}
	return whole + "." + fraction, nil
}
func validTemperature(v *float64) bool {
	return v == nil || !math.IsNaN(*v) && !math.IsInf(*v, 0) && math.Abs(*v) <= 9999.99 && math.Abs(*v*100-math.Round(*v*100)) < 0.000001
}
func text(fields map[string]string, key, value string, min, max int) {
	if !input.Text(value, min, max) {
		fields[key] = "Completa este campo y respeta la longitud indicada."
	}
}
func id(fields map[string]string, key, value string) {
	if !ValidID(value) {
		fields[key] = "Selecciona un registro válido."
	}
}
func done(fields map[string]string) error {
	if len(fields) > 0 {
		return &ValidationError{fields}
	}
	return nil
}
func NormalizeSupplier(v Supplier) (Supplier, error) {
	v.ID = strings.ToLower(v.ID)
	v.Name = strings.TrimSpace(v.Name)
	v.TaxID = strings.TrimSpace(v.TaxID)
	v.ContactName = strings.TrimSpace(v.ContactName)
	v.ContactPhone = strings.TrimSpace(v.ContactPhone)
	v.ContactEmail = strings.TrimSpace(v.ContactEmail)
	v.Address = strings.TrimSpace(v.Address)
	f := map[string]string{}
	id(f, "id", v.ID)
	text(f, "name", v.Name, 2, 200)
	text(f, "taxId", v.TaxID, 0, 20)
	text(f, "address", v.Address, 0, 300)
	if !input.PersonName(v.ContactName, 0, 160) {
		f["contactName"] = "Usa letras, espacios, apóstrofos o guiones; sin números."
	}
	if !input.Phone(v.ContactPhone) {
		f["contactPhone"] = "Usa entre 7 y 15 dígitos, con formato de teléfono."
	}
	if v.ContactEmail != "" && !input.Email(v.ContactEmail) {
		f["contactEmail"] = "Ingresa un correo válido."
	}
	return v, done(f)
}
func validatePolicy(f map[string]string, requires bool, min, max *float64) {
	if !validTemperature(min) || !validTemperature(max) || requires && (min == nil || max == nil) || min != nil && max != nil && *min > *max || !requires && (min != nil || max != nil) {
		f["minimumTemperature"] = "Define mínimo y máximo del procedimiento, en orden y con hasta dos decimales."
	}
}
func NormalizeProduct(v Product) (Product, error) {
	v.ID = strings.ToLower(v.ID)
	v.Name = strings.TrimSpace(v.Name)
	v.Category = strings.TrimSpace(v.Category)
	v.Unit = strings.TrimSpace(v.Unit)
	f := map[string]string{}
	id(f, "id", v.ID)
	text(f, "name", v.Name, 2, 200)
	text(f, "category", v.Category, 0, 100)
	text(f, "unit", v.Unit, 1, 24)
	validatePolicy(f, v.RequiresTemperature, v.MinimumTemperature, v.MaximumTemperature)
	v.RowVersion = 1
	return v, done(f)
}
func ValidatePolicy(v TemperaturePolicy) error {
	f := map[string]string{}
	if v.RowVersion < 1 {
		f["rowVersion"] = "Consulta el producto antes de actualizar el criterio."
	}
	validatePolicy(f, v.RequiresTemperature, v.MinimumTemperature, v.MaximumTemperature)
	return done(f)
}
func timestamp(f map[string]string, key string, value, now time.Time) {
	if value.IsZero() || value.After(now.Add(5*time.Minute)) {
		f[key] = "Indica una fecha y hora válidas, sin adelantarlas a la hora actual."
	}
}
func NormalizeReceipt(v Receipt, now time.Time) (Receipt, error) {
	v.Items = slices.Clone(v.Items)
	f := map[string]string{}
	id(f, "id", v.ID)
	id(f, "locationId", v.LocationID)
	id(f, "supplierId", v.SupplierID)
	id(f, "clientOperationId", v.ClientOperationID)
	v.ID = strings.ToLower(v.ID)
	v.LocationID = strings.ToLower(v.LocationID)
	v.SupplierID = strings.ToLower(v.SupplierID)
	v.ClientOperationID = strings.ToLower(v.ClientOperationID)
	v.DocumentReference = strings.TrimSpace(v.DocumentReference)
	v.Notes = strings.TrimSpace(v.Notes)
	v.ReceivedAt = v.ReceivedAt.UTC().Truncate(time.Microsecond)
	v.ClientCreatedAt = v.ClientCreatedAt.UTC().Truncate(time.Microsecond)
	timestamp(f, "receivedAt", v.ReceivedAt, now)
	timestamp(f, "clientCreatedAt", v.ClientCreatedAt, now)
	text(f, "documentReference", v.DocumentReference, 0, 100)
	text(f, "notes", v.Notes, 0, 2000)
	if len(v.Items) < 1 || len(v.Items) > 50 {
		f["items"] = "Incluye entre 1 y 50 productos recibidos."
	}
	seen := map[string]bool{}
	for i := range v.Items {
		a := &v.Items[i]
		prefix := fmt.Sprintf("items.%d.", i)
		id(f, prefix+"id", a.ID)
		id(f, prefix+"productId", a.ProductID)
		a.ID = strings.ToLower(a.ID)
		a.ProductID = strings.ToLower(a.ProductID)
		if seen[a.ID] {
			f[prefix+"id"] = "Cada producto recibido necesita un identificador único."
		}
		seen[a.ID] = true
		a.BatchCode = strings.TrimSpace(a.BatchCode)
		a.RejectionReason = strings.TrimSpace(a.RejectionReason)
		a.StorageLocation = strings.TrimSpace(a.StorageLocation)
		a.Label = strings.TrimSpace(a.Label)
		text(f, prefix+"batchCode", a.BatchCode, 1, 100)
		text(f, prefix+"rejectionReason", a.RejectionReason, 0, 2000)
		text(f, prefix+"storageLocation", a.StorageLocation, 0, 120)
		text(f, prefix+"label", a.Label, 0, 160)
		if q, err := Quantity(a.Quantity); err != nil {
			f[prefix+"quantity"] = "Ingresa una cantidad mayor que cero, con hasta tres decimales."
		} else {
			a.Quantity = q
		}
		if a.ExpirationDate != "" {
			if d, e := time.Parse("2006-01-02", a.ExpirationDate); e != nil || d.Format("2006-01-02") != a.ExpirationDate {
				f[prefix+"expirationDate"] = "Usa una fecha válida: AAAA-MM-DD."
			}
		}
		if !validTemperature(a.TemperatureCelsius) {
			f[prefix+"temperatureCelsius"] = "Usa una medición válida con hasta dos decimales."
		}
		if a.PackagingCondition != "INTACT" && a.PackagingCondition != "DAMAGED" {
			f[prefix+"packagingCondition"] = "Indica si el envase está íntegro o dañado."
		}
		switch a.Decision {
		case "ACCEPTED":
			if a.StorageLocation == "" {
				f[prefix+"storageLocation"] = "Indica dónde se almacenará el lote."
			}
			if a.PackagingCondition == "DAMAGED" {
				f[prefix+"decision"] = "No aceptes un producto con el envase dañado."
			}
		case "REJECTED", "OBSERVED":
			if a.RejectionReason == "" {
				f[prefix+"rejectionReason"] = "Explica el rechazo o la observación."
			}
			if a.StorageLocation != "" || a.Label != "" {
				f[prefix+"storageLocation"] = "Solo los productos aceptados pasan a almacenamiento."
			}
		default:
			f[prefix+"decision"] = "Elige aceptar, rechazar o dejar observado."
		}
		a.MinimumTemperature = nil
		a.MaximumTemperature = nil
		a.ProductName = ""
		a.Unit = ""
	}
	v.RequestHash = ""
	v.ReceivedBy = ""
	v.ReceivedByName = ""
	v.SupplierName = ""
	v.CreatedAt = time.Time{}
	return v, done(f)
}
func ValidateReceiptItem(v ReceiptItem, p Product, localDay string) error {
	f := map[string]string{}
	if p.RequiresTemperature && v.TemperatureCelsius == nil {
		f["temperatureCelsius"] = "Este producto requiere registrar su temperatura."
	}
	if v.Decision == "ACCEPTED" {
		if v.PackagingCondition != "INTACT" {
			f["packagingCondition"] = "No aceptes un producto con envase dañado."
		}
		if v.ExpirationDate != "" && v.ExpirationDate < localDay {
			f["expirationDate"] = "El producto ya estaba vencido en la fecha de recepción."
		}
		if p.RequiresTemperature && (p.MinimumTemperature == nil || p.MaximumTemperature == nil) {
			f["productId"] = "El administrador debe configurar el criterio de temperatura del producto."
		}
		if p.RequiresTemperature && p.MinimumTemperature != nil && p.MaximumTemperature != nil && v.TemperatureCelsius != nil && (*v.TemperatureCelsius < *p.MinimumTemperature || *v.TemperatureCelsius > *p.MaximumTemperature) {
			f["decision"] = "La medición está fuera del criterio del producto. Registra rechazo u observación."
		}
	}
	return done(f)
}
func NormalizePlan(v CleaningPlan) (CleaningPlan, error) {
	f := map[string]string{}
	id(f, "id", v.ID)
	id(f, "locationId", v.LocationID)
	v.ID = strings.ToLower(v.ID)
	v.LocationID = strings.ToLower(v.LocationID)
	v.Area = strings.TrimSpace(v.Area)
	v.Task = strings.TrimSpace(v.Task)
	v.Instructions = strings.TrimSpace(v.Instructions)
	v.ProductName = strings.TrimSpace(v.ProductName)
	text(f, "area", v.Area, 2, 100)
	text(f, "task", v.Task, 2, 240)
	text(f, "instructions", v.Instructions, 0, 2000)
	text(f, "productName", v.ProductName, 0, 160)
	if v.Frequency != "DAILY" && v.Frequency != "WEEKLY" && v.Frequency != "MONTHLY" {
		f["frequency"] = "Elige diaria, semanal o mensual; los turnos aún no están configurados."
	}
	if v.DueTime != "" {
		if d, e := time.Parse("15:04", v.DueTime); e != nil || d.Format("15:04") != v.DueTime {
			f["dueTime"] = "Usa hora válida: HH:mm."
		}
	}
	v.Active = true
	return v, done(f)
}
func NormalizeExecution(v CleaningExecution, now time.Time) (CleaningExecution, error) {
	f := map[string]string{}
	id(f, "id", v.ID)
	id(f, "locationId", v.LocationID)
	id(f, "cleaningPlanId", v.CleaningPlanID)
	id(f, "clientOperationId", v.ClientOperationID)
	v.ID = strings.ToLower(v.ID)
	v.LocationID = strings.ToLower(v.LocationID)
	v.CleaningPlanID = strings.ToLower(v.CleaningPlanID)
	v.ClientOperationID = strings.ToLower(v.ClientOperationID)
	v.ExecutedAt = v.ExecutedAt.UTC().Truncate(time.Microsecond)
	v.ClientCreatedAt = v.ClientCreatedAt.UTC().Truncate(time.Microsecond)
	v.Observation = strings.TrimSpace(v.Observation)
	timestamp(f, "executedAt", v.ExecutedAt, now)
	timestamp(f, "clientCreatedAt", v.ClientCreatedAt, now)
	text(f, "observation", v.Observation, 0, 2000)
	if v.Result != "COMPLETED" && v.Result != "NOT_COMPLETED" && v.Result != "OBSERVED" {
		f["result"] = "Indica si se realizó, no se realizó o quedó observada."
	}
	if v.Result != "COMPLETED" && v.Observation == "" {
		f["observation"] = "Explica por qué no se completó o qué observaste."
	}
	v.RequestHash = ""
	v.Area = ""
	v.Task = ""
	v.ResponsibleUserID = ""
	v.ResponsibleName = ""
	v.CreatedAt = time.Time{}
	return v, done(f)
}
