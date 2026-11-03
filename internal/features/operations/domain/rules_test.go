package domain

import (
	"errors"
	"testing"
	"time"
)

const testID = "10000000-0000-0000-0000-000000000001"

func TestQuantityIsExactAndBounded(t *testing.T) {
	for in, want := range map[string]string{"1": "1", "1.000": "1", "0.125": "0.125", "999999999.999": "999999999.999"} {
		got, err := Quantity(in)
		if err != nil || got != want {
			t.Fatal(in, got, err)
		}
	}
	for _, in := range []string{"0", "-1", "1.0001", "1e3", "NaN", "1000000000", "1,5", "00.1"} {
		if _, err := Quantity(in); err == nil {
			t.Fatal("cantidad inválida aceptada", in)
		}
	}
}
func TestReceptionUsesProcedureAndLocalReceivingDate(t *testing.T) {
	minimum, maximum := 0.0, 4.0
	temperature := 3.0
	product := Product{ID: testID, Name: "Leche", Unit: "L", RequiresTemperature: true, MinimumTemperature: &minimum, MaximumTemperature: &maximum}
	item := ReceiptItem{ID: "20000000-0000-0000-0000-000000000001", ProductID: testID, BatchCode: "Lote 1", ExpirationDate: "2026-11-01", Quantity: "2.500", TemperatureCelsius: &temperature, Decision: "ACCEPTED", PackagingCondition: "INTACT", StorageLocation: "Frío 1"}
	// 2 de noviembre UTC, todavía 1 de noviembre en Lima.
	received := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	zone, _ := time.LoadLocation("America/Lima")
	if err := ValidateReceiptItem(item, product, received.In(zone).Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ReceiptItem){func(v *ReceiptItem) { v.PackagingCondition = "DAMAGED" }, func(v *ReceiptItem) { v.ExpirationDate = "2026-10-31" }, func(v *ReceiptItem) { v.TemperatureCelsius = nil }, func(v *ReceiptItem) { high := 5.0; v.TemperatureCelsius = &high }} {
		bad := item
		change(&bad)
		if err := ValidateReceiptItem(bad, product, "2026-11-01"); err == nil {
			t.Fatal("aceptación inválida", bad)
		}
	}
	product.MinimumTemperature = nil
	product.MaximumTemperature = nil
	if err := ValidateReceiptItem(item, product, "2026-11-01"); err == nil {
		t.Fatal("inventó criterio para producto heredado")
	}
	item.Decision = "OBSERVED"
	item.RejectionReason = "Requiere evaluación"
	item.StorageLocation = ""
	if err := ValidateReceiptItem(item, product, "2026-11-01"); err != nil {
		t.Fatal("impide documentar observación sin criterio", err)
	}
}
func TestPackagingAndReasonsAreRequiredForNewRecords(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	base := Receipt{ID: testID, LocationID: testID, SupplierID: testID, ReceivedAt: now, ClientOperationID: testID, ClientCreatedAt: now, Items: []ReceiptItem{{ID: testID, ProductID: testID, BatchCode: "L1", Quantity: "1", Decision: "REJECTED", PackagingCondition: "DAMAGED"}}}
	_, err := NormalizeReceipt(base, now)
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Fields["items.0.rejectionReason"] == "" {
		t.Fatal("falta motivo no detectado", err)
	}
	base.Items[0].RejectionReason = "Envase roto"
	if _, err = NormalizeReceipt(base, now); err != nil {
		t.Fatal(err)
	}
	base.Items[0].PackagingCondition = ""
	if _, err = NormalizeReceipt(base, now); err == nil {
		t.Fatal("registro nuevo sin envase")
	}
}
func TestRolesSeparateStockFromAssignedCleaningTasks(t *testing.T) {
	if CanReceive("WORKER") || CanReadStock("WORKER") || CanConfigure("CHEF") || CanExecuteCleaning("ADMIN") {
		t.Fatal("privilegio no documentado")
	}
	if !CanExecuteCleaning("WORKER") || !CanReceive("CHEF") || !CanConfigure("SUPER_ADMIN") {
		t.Fatal("rol previsto denegado")
	}
}
