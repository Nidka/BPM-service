package operationshttp

import (
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"strings"
	"time"
)

type supplierDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TaxID        string `json:"taxId"`
	ContactName  string `json:"contactName"`
	ContactPhone string `json:"contactPhone"`
	ContactEmail string `json:"contactEmail"`
	Address      string `json:"address"`
}

func (v supplierDTO) domain() domain.Supplier {
	return domain.Supplier{ID: v.ID, Name: v.Name, TaxID: v.TaxID, ContactName: v.ContactName, ContactPhone: v.ContactPhone, ContactEmail: v.ContactEmail, Address: v.Address}
}
func supplier(v domain.Supplier) supplierDTO {
	return supplierDTO{v.ID, v.Name, v.TaxID, v.ContactName, v.ContactPhone, v.ContactEmail, v.Address}
}

type productInput struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Category            string   `json:"category"`
	Unit                string   `json:"unit"`
	RequiresTemperature bool     `json:"requiresTemperature"`
	MinimumTemperature  *float64 `json:"minimumTemperature"`
	MaximumTemperature  *float64 `json:"maximumTemperature"`
}

func (v productInput) domain() domain.Product {
	return domain.Product{ID: v.ID, Name: v.Name, Category: v.Category, Unit: v.Unit, RequiresTemperature: v.RequiresTemperature, MinimumTemperature: v.MinimumTemperature, MaximumTemperature: v.MaximumTemperature}
}

type productDTO struct {
	productInput
	RowVersion int `json:"rowVersion"`
}

func product(v domain.Product) productDTO {
	return productDTO{productInput{v.ID, v.Name, v.Category, v.Unit, v.RequiresTemperature, v.MinimumTemperature, v.MaximumTemperature}, v.RowVersion}
}

type policyInput struct {
	RowVersion          int      `json:"rowVersion"`
	RequiresTemperature bool     `json:"requiresTemperature"`
	MinimumTemperature  *float64 `json:"minimumTemperature"`
	MaximumTemperature  *float64 `json:"maximumTemperature"`
}

func (v policyInput) domain() domain.TemperaturePolicy {
	return domain.TemperaturePolicy{RowVersion: v.RowVersion, RequiresTemperature: v.RequiresTemperature, MinimumTemperature: v.MinimumTemperature, MaximumTemperature: v.MaximumTemperature}
}

type receiptHeader struct {
	ID                string    `json:"id"`
	LocationID        string    `json:"locationId"`
	SupplierID        string    `json:"supplierId"`
	ReceivedAt        time.Time `json:"receivedAt"`
	DocumentReference string    `json:"documentReference"`
	Notes             string    `json:"notes"`
	ClientOperationID string    `json:"clientOperationId"`
	ClientCreatedAt   time.Time `json:"clientCreatedAt"`
}
type receiptItemInput struct {
	ID                 string   `json:"id"`
	ProductID          string   `json:"productId"`
	BatchCode          string   `json:"batchCode"`
	ExpirationDate     *string  `json:"expirationDate"`
	Quantity           string   `json:"quantity"`
	TemperatureCelsius *float64 `json:"temperatureCelsius"`
	Decision           string   `json:"decision"`
	RejectionReason    string   `json:"rejectionReason"`
	PackagingCondition string   `json:"packagingCondition"`
	StorageLocation    string   `json:"storageLocation"`
	Label              string   `json:"label"`
}

func (v receiptItemInput) domain() domain.ReceiptItem {
	return domain.ReceiptItem{ID: v.ID, ProductID: v.ProductID, BatchCode: v.BatchCode, ExpirationDate: stringValue(v.ExpirationDate), Quantity: v.Quantity, TemperatureCelsius: v.TemperatureCelsius, Decision: v.Decision, RejectionReason: v.RejectionReason, PackagingCondition: v.PackagingCondition, StorageLocation: v.StorageLocation, Label: v.Label}
}

type receiptInput struct {
	receiptHeader
	Items []receiptItemInput `json:"items"`
}

func (v receiptInput) domain() domain.Receipt {
	items := make([]domain.ReceiptItem, 0, len(v.Items))
	for _, item := range v.Items {
		items = append(items, item.domain())
	}
	return domain.Receipt{ID: v.ID, LocationID: v.LocationID, SupplierID: v.SupplierID, ReceivedAt: v.ReceivedAt, DocumentReference: v.DocumentReference, Notes: v.Notes, ClientOperationID: v.ClientOperationID, ClientCreatedAt: v.ClientCreatedAt, Items: items}
}

type receiptItemDTO struct {
	ID                 string   `json:"id"`
	ProductID          string   `json:"productId"`
	ProductName        string   `json:"productName"`
	Unit               string   `json:"unit"`
	BatchCode          string   `json:"batchCode"`
	ExpirationDate     *string  `json:"expirationDate"`
	Quantity           string   `json:"quantity"`
	TemperatureCelsius *float64 `json:"temperatureCelsius"`
	MinimumTemperature *float64 `json:"minimumTemperature"`
	MaximumTemperature *float64 `json:"maximumTemperature"`
	Decision           string   `json:"decision"`
	RejectionReason    string   `json:"rejectionReason"`
	PackagingCondition *string  `json:"packagingCondition"`
	StorageLocation    string   `json:"storageLocation"`
	Label              string   `json:"label"`
}
type receiptDTO struct {
	receiptHeader
	SupplierName   string           `json:"supplierName"`
	ReceivedBy     string           `json:"receivedBy"`
	ReceivedByName string           `json:"receivedByName"`
	CreatedAt      time.Time        `json:"createdAt"`
	Items          []receiptItemDTO `json:"items"`
}

func receipt(v domain.Receipt) receiptDTO {
	items := make([]receiptItemDTO, 0, len(v.Items))
	for _, i := range v.Items {
		items = append(items, receiptItemDTO{i.ID, i.ProductID, i.ProductName, i.Unit, i.BatchCode, optional(i.ExpirationDate), canonicalQuantity(i.Quantity), i.TemperatureCelsius, i.MinimumTemperature, i.MaximumTemperature, i.Decision, i.RejectionReason, optional(i.PackagingCondition), i.StorageLocation, i.Label})
	}
	return receiptDTO{receiptHeader{v.ID, v.LocationID, v.SupplierID, v.ReceivedAt, v.DocumentReference, v.Notes, v.ClientOperationID, v.ClientCreatedAt}, v.SupplierName, v.ReceivedBy, v.ReceivedByName, v.CreatedAt, items}
}

type lotDTO struct {
	ID                string    `json:"id"`
	LocationID        string    `json:"locationId"`
	ReceiptItemID     string    `json:"receiptItemId"`
	ProductID         string    `json:"productId"`
	ProductName       string    `json:"productName"`
	Unit              string    `json:"unit"`
	BatchCode         string    `json:"batchCode"`
	ExpirationDate    *string   `json:"expirationDate"`
	ReceivedAt        time.Time `json:"receivedAt"`
	StorageLocation   string    `json:"storageLocation"`
	Label             string    `json:"label"`
	QuantityAvailable string    `json:"quantityAvailable"`
	Status            string    `json:"status"`
	Expired           bool      `json:"expired"`
}

func lot(v domain.Lot) lotDTO {
	return lotDTO{v.ID, v.LocationID, v.ReceiptItemID, v.ProductID, v.ProductName, v.Unit, v.BatchCode, optional(v.ExpirationDate), v.ReceivedAt, v.StorageLocation, v.Label, canonicalQuantity(v.QuantityAvailable), v.Status, v.Expired}
}

type planInput struct {
	ID           string `json:"id"`
	LocationID   string `json:"locationId"`
	Area         string `json:"area"`
	Task         string `json:"task"`
	Instructions string `json:"instructions"`
	ProductName  string `json:"productName"`
	Frequency    string `json:"frequency"`
	DueTime      string `json:"dueTime"`
}

func (v planInput) domain() domain.CleaningPlan {
	return domain.CleaningPlan{ID: v.ID, LocationID: v.LocationID, Area: v.Area, Task: v.Task, Instructions: v.Instructions, ProductName: v.ProductName, Frequency: v.Frequency, DueTime: v.DueTime}
}

type planDTO struct {
	planInput
	Active bool `json:"active"`
}

func plan(v domain.CleaningPlan) planDTO {
	return planDTO{planInput{v.ID, v.LocationID, v.Area, v.Task, v.Instructions, v.ProductName, v.Frequency, v.DueTime}, v.Active}
}

type executionInput struct {
	ID                string    `json:"id"`
	LocationID        string    `json:"locationId"`
	CleaningPlanID    string    `json:"cleaningPlanId"`
	ExecutedAt        time.Time `json:"executedAt"`
	Result            string    `json:"result"`
	Observation       string    `json:"observation"`
	ClientOperationID string    `json:"clientOperationId"`
	ClientCreatedAt   time.Time `json:"clientCreatedAt"`
}

func (v executionInput) domain() domain.CleaningExecution {
	return domain.CleaningExecution{ID: v.ID, LocationID: v.LocationID, CleaningPlanID: v.CleaningPlanID, ExecutedAt: v.ExecutedAt, Result: v.Result, Observation: v.Observation, ClientOperationID: v.ClientOperationID, ClientCreatedAt: v.ClientCreatedAt}
}

type executionDTO struct {
	executionInput
	Area              string    `json:"area"`
	Task              string    `json:"task"`
	ResponsibleUserID string    `json:"responsibleUserId"`
	ResponsibleName   string    `json:"responsibleName"`
	CreatedAt         time.Time `json:"createdAt"`
}

func execution(v domain.CleaningExecution) executionDTO {
	return executionDTO{executionInput{v.ID, v.LocationID, v.CleaningPlanID, v.ExecutedAt, v.Result, v.Observation, v.ClientOperationID, v.ClientCreatedAt}, v.Area, v.Task, v.ResponsibleUserID, v.ResponsibleName, v.CreatedAt}
}
func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func canonicalQuantity(v string) string {
	if strings.Contains(v, ".") {
		v = strings.TrimRight(strings.TrimRight(v, "0"), ".")
	}
	return v
}
