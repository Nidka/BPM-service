package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"strings"
)

func (s *Service) CreateCatalog(ctx context.Context, user, org, key string, input domain.Catalog) (domain.Catalog, error) {
	v, e := domain.NormalizeCatalog(input, true)
	if e != nil {
		return v, e
	}
	return write(ctx, s, user, org, "catalog", key, "", v, func(_ Store, a domain.Actor) error {
		if !domain.CanConfigure(a.Role) {
			return domain.ErrDenied
		}
		return nil
	}, func(db Store, a domain.Actor, _ string) (domain.Catalog, error) {
		v.VersionNumber = 1
		out, e := db.InsertCatalog(ctx, a, org, v)
		if e != nil {
			return out, e
		}
		return out, db.Audit(ctx, a, org, "incident_catalog.created", v.ID)
	})
}
func (s *Service) VersionCatalog(ctx context.Context, user, org, id, key string, input domain.CatalogVersion) (domain.Catalog, error) {
	id = strings.ToLower(id)
	if !domain.ValidID(id) {
		return domain.Catalog{}, domain.ErrNotFound
	}
	v, e := domain.NormalizeCatalog(input.Catalog, false)
	if e != nil {
		return v, e
	}
	if input.ExpectedVersion < 1 {
		return v, &domain.ValidationError{Fields: map[string]string{"expectedVersion": "Actualiza el catálogo antes de publicar."}}
	}
	input.Catalog = v
	return write(ctx, s, user, org, "catalog-version:"+id, key, "", input, func(_ Store, a domain.Actor) error {
		if !domain.CanConfigure(a.Role) {
			return domain.ErrDenied
		}
		return nil
	}, func(db Store, a domain.Actor, _ string) (domain.Catalog, error) {
		base, e := db.Catalog(ctx, org, id)
		if e != nil {
			return v, e
		}
		latest, e := db.Latest(ctx, org, base.Code)
		if e != nil {
			return v, e
		}
		if latest.ID != base.ID || latest.VersionNumber != input.ExpectedVersion {
			return v, domain.ErrConflict
		}
		v.Code = base.Code
		v.VersionNumber = base.VersionNumber + 1
		out, e := db.InsertCatalog(ctx, a, org, v)
		if e != nil {
			return out, e
		}
		return out, db.Audit(ctx, a, org, "incident_catalog.versioned", v.ID)
	})
}
