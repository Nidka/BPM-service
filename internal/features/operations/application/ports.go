package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

type CatalogStore interface {
	Supplier(context.Context, string, string) (domain.Supplier, error)
	Product(context.Context, string, string) (domain.Product, error)
	CreateSupplier(context.Context, domain.Actor, string, domain.Supplier) (domain.Supplier, error)
	CreateProduct(context.Context, domain.Actor, string, domain.Product) (domain.Product, error)
	UpdatePolicy(context.Context, domain.Actor, string, string, domain.TemperaturePolicy) (domain.Product, error)
	Suppliers(context.Context, string, pagination.Window) ([]domain.Supplier, error)
	Products(context.Context, string, pagination.Window) ([]domain.Product, error)
}
type ReceiptStore interface {
	CreateReceipt(context.Context, domain.Actor, string, domain.Receipt) (domain.Receipt, error)
	Receipt(context.Context, string, string) (domain.Receipt, error)
	Receipts(context.Context, string, string, pagination.Window) ([]domain.Receipt, error)
	Lots(context.Context, string, string, pagination.Window) ([]domain.Lot, error)
}
type CleaningStore interface {
	Plan(context.Context, string, string) (domain.CleaningPlan, error)
	CreatePlan(context.Context, domain.Actor, string, domain.CleaningPlan) (domain.CleaningPlan, error)
	CreateExecution(context.Context, domain.Actor, string, domain.CleaningExecution) (domain.CleaningExecution, error)
	Execution(context.Context, string, string) (domain.CleaningExecution, error)
	Plans(context.Context, string, string, pagination.Window) ([]domain.CleaningPlan, error)
	Executions(context.Context, domain.Actor, string, string, pagination.Window) ([]domain.CleaningExecution, error)
}

// Store reúne solo los puertos que comparten esta transacción funcional.
type Store interface {
	CatalogStore
	ReceiptStore
	CleaningStore
	Actor(context.Context, string, string) (domain.Actor, error)
	Location(context.Context, domain.Actor, string, string) (string, error)
	Lock(context.Context, string) error
	Replay(context.Context, string, string, string) ([]byte, bool, error)
	Remember(context.Context, domain.Actor, string, string, string, []byte) error
	Audit(context.Context, domain.Actor, string, string, string) error
}
type UnitOfWork interface {
	Within(context.Context, func(Store) error) error
}
