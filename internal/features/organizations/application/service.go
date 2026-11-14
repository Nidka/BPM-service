package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	inputformat "github.com/Nidka/BPM-service/internal/shared/domain/input"
	"regexp"
	"strings"

	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
)

// Store pertenece a una transacción. La autorización y las escrituras comparten
// bloqueos para impedir que se sustituya la membresía por la de otra organización.
type Store interface {
	Actor(context.Context, string) (domain.Actor, error)
	OrganizationsPage(context.Context, domain.Actor, pagination.Window) ([]domain.Organization, error)
	LocationsPage(context.Context, domain.Actor, string, string, pagination.Window) ([]domain.Location, error)
	MembersPage(context.Context, string, pagination.Window) ([]domain.Member, error)
	EntriesPage(context.Context, domain.Actor, string, string, string, pagination.Window) ([]domain.Entry, error)
	Organizations(context.Context, domain.Actor) ([]domain.Organization, error)
	Role(context.Context, domain.Actor, string) (string, error)
	WorkspacePage(context.Context, domain.Actor, string, string, int) (domain.Workspace, error)
	Workspace(context.Context, domain.Actor, string, string) (domain.Workspace, error)
	CreateOrganization(context.Context, domain.Actor, domain.Organization) error
	CreateLocation(context.Context, domain.Actor, string, domain.Location) error
	AddMember(context.Context, domain.Actor, string, string, string, []string) error
	MemberEligibility(context.Context, string, string) (domain.MemberEligibility, error)
	MemberAccountSuggestions(context.Context, string, string) ([]string, error)
}
type UnitOfWork interface {
	Within(context.Context, func(Store) error) error
}
type Service struct{ work UnitOfWork }

func New(work UnitOfWork) *Service { return &Service{work: work} }

func (s *Service) List(ctx context.Context, uid string) (result []domain.Organization, err error) {
	result = []domain.Organization{}
	err = s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		result, e = db.Organizations(ctx, actor)
		return e
	})
	return
}
func (s *Service) Workspace(ctx context.Context, uid, org string) (result domain.Workspace, err error) {
	err = s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		role, e := db.Role(ctx, actor, org)
		if e != nil {
			return e
		}
		result, e = db.Workspace(ctx, actor, org, role)
		return e
	})
	return
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validateText(fields map[string]string, field, value string, min, max int) {
	if !inputformat.Text(value, min, max) {
		fields[field] = "Revisa este campo: es obligatorio o supera la longitud permitida."
	}
}
func (s *Service) CreateOrganization(ctx context.Context, uid string, input domain.Organization) (domain.Organization, error) {
	fields := map[string]string{}
	if !uuid.MatchString(input.ID) {
		fields["id"] = "Identificador de operación inválido."
	}
	validateText(fields, "legalName", input.LegalName, 2, 200)
	validateText(fields, "tradeName", input.TradeName, 0, 200)
	validateText(fields, "taxId", input.TaxID, 0, 20)
	if !inputformat.PersonName(input.ContactName, 0, 160) {
		fields["contactName"] = "Usa letras, espacios, apóstrofos o guiones; sin números (hasta 160 caracteres)."
	}
	if !inputformat.Phone(input.ContactPhone) {
		fields["contactPhone"] = "Usa entre 7 y 15 dígitos; puedes incluir + al inicio, espacios, guiones y paréntesis."
	}
	if !inputformat.Text(input.ContactEmail, 0, 320) || len(input.ContactEmail) > 320 ||
		strings.TrimSpace(input.ContactEmail) != "" && !inputformat.Email(input.ContactEmail) {
		fields["contactEmail"] = "Ingresa un correo válido."
	}
	switch input.BusinessType {
	case "RESTAURANT", "CAFETERIA", "CATERING", "RELATED_SERVICE":
	default:
		fields["businessType"] = "Selecciona un tipo de establecimiento."
	}
	if len(fields) > 0 {
		return domain.Organization{}, &domain.ValidationError{Fields: fields}
	}
	input.LegalName = strings.TrimSpace(input.LegalName)
	input.TradeName = strings.TrimSpace(input.TradeName)
	err := s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		if actor.Role != "SUPER_ADMIN" {
			return domain.ErrDenied
		}
		return db.CreateOrganization(ctx, actor, input)
	})
	return input, err
}
func (s *Service) CreateLocation(ctx context.Context, uid, org string, input domain.Location) (domain.Location, error) {
	fields := map[string]string{}
	if !uuid.MatchString(input.ID) {
		fields["id"] = "Identificador de operación inválido."
	}
	validateText(fields, "name", input.Name, 2, 160)
	validateText(fields, "address", input.Address, 3, 300)
	validateText(fields, "district", input.District, 0, 120)
	if !inputformat.Phone(input.ContactPhone) {
		fields["contactPhone"] = "Usa entre 7 y 15 dígitos; puedes incluir + al inicio, espacios, guiones y paréntesis."
	}
	input.Timezone = "America/Lima"
	if len(fields) > 0 {
		return domain.Location{}, &domain.ValidationError{Fields: fields}
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Address = strings.TrimSpace(input.Address)
	err := s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		role, e := db.Role(ctx, actor, org)
		if e != nil {
			return e
		}
		if !domain.CanManage(role) {
			return domain.ErrDenied
		}
		return db.CreateLocation(ctx, actor, org, input)
	})
	return input, err
}
func (s *Service) AddMember(ctx context.Context, uid, org, email, role string, locations []string) error {
	fields := map[string]string{}
	if !inputformat.Email(email) {
		fields["email"] = "Ingresa el correo de una cuenta BPM existente."
	}
	if role != "ADMIN" && role != "CHEF" && role != "WORKER" {
		fields["role"] = "Selecciona un rol válido."
	}
	if len(locations) > 100 {
		fields["locationIds"] = "Selecciona como máximo 100 sedes."
	}
	for _, id := range locations {
		if !uuid.MatchString(id) {
			fields["locationIds"] = "Selecciona sedes válidas."
		}
	}
	if role != "ADMIN" && len(locations) == 0 {
		fields["locationIds"] = "Asigna al menos una sede."
	}
	if len(fields) > 0 {
		return &domain.ValidationError{Fields: fields}
	}
	email = strings.TrimSpace(email)
	return s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		access, e := db.Role(ctx, actor, org)
		if e != nil {
			return e
		}
		if !domain.CanManage(access) {
			return domain.ErrDenied
		}
		return db.AddMember(ctx, actor, org, email, role, locations)
	})
}
