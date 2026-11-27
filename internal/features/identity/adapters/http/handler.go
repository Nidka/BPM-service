package identityhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/identity/domain"
)

type CurrentProfileGetter interface {
	Execute(ctx context.Context, identity contracts.Identity) (domain.Profile, error)
}

// OnboardingSetter marca (true) o desmarca (false) la introducción de la persona autenticada.
type OnboardingSetter interface {
	Execute(ctx context.Context, identity contracts.Identity, seen bool) (*time.Time, error)
}

type Handler struct {
	getCurrentProfile CurrentProfileGetter
	setOnboarding     OnboardingSetter
	logger            *slog.Logger
}

// WithOnboarding habilita PUT y DELETE /me/onboarding.
func (handler *Handler) WithOnboarding(setter OnboardingSetter) *Handler {
	handler.setOnboarding = setter
	return handler
}

// MarkOnboardingSeen guarda que la persona terminó u omitió la introducción.
func (handler *Handler) MarkOnboardingSeen(writer http.ResponseWriter, request *http.Request) {
	handler.onboarding(writer, request, true)
}

// ClearOnboardingSeen permite volver a ver la introducción desde Ayuda.
func (handler *Handler) ClearOnboardingSeen(writer http.ResponseWriter, request *http.Request) {
	handler.onboarding(writer, request, false)
}

func (handler *Handler) onboarding(writer http.ResponseWriter, request *http.Request, seen bool) {
	requestID := middleware.RequestIDFromContext(request.Context())
	identity, ok := middleware.IdentityFromContext(request.Context())
	if !ok {
		response.Unauthenticated(writer, requestID)
		return
	}
	seenAt, err := handler.setOnboarding.Execute(request.Context(), identity, seen)
	if err != nil {
		writeIdentityError(writer, request, err, handler.logger)
		return
	}
	response.JSON(writer, http.StatusOK, map[string]any{"data": map[string]any{"onboardingSeenAt": formatSeenAt(seenAt)}})
}

func formatSeenAt(seenAt *time.Time) *string {
	if seenAt == nil {
		return nil
	}
	value := seenAt.UTC().Format(time.RFC3339)
	return &value
}

func NewHandler(getCurrentProfile CurrentProfileGetter, logger *slog.Logger) *Handler {
	return &Handler{getCurrentProfile: getCurrentProfile, logger: logger}
}

func (handler *Handler) Me(writer http.ResponseWriter, request *http.Request) {
	requestID := middleware.RequestIDFromContext(request.Context())
	identity, ok := middleware.IdentityFromContext(request.Context())
	if !ok {
		response.Unauthenticated(writer, requestID)
		return
	}

	profile, err := handler.getCurrentProfile.Execute(request.Context(), identity)
	if err != nil {
		writeIdentityError(writer, request, err, handler.logger)
		return
	}
	response.JSON(writer, http.StatusOK, map[string]any{"data": newProfileResponse(profile)})
}

func writeIdentityError(writer http.ResponseWriter, request *http.Request, err error, logger *slog.Logger) {
	requestID := middleware.RequestIDFromContext(request.Context())
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		response.ValidationError(writer, requestID, validation.Fields)
	case errors.Is(err, domain.ErrEmailUnverified):
		response.Error(writer, http.StatusForbidden, requestID, response.APIError{Code: "EMAIL_NOT_VERIFIED", Message: "Verifica tu correo antes de completar el registro."})
	case errors.Is(err, domain.ErrRegistrationConflict):
		response.Error(writer, http.StatusConflict, requestID, response.APIError{Code: "REGISTRATION_CONFLICT", Message: "No se pudo completar el registro. Usa tu método de acceso original o contacta al administrador."})
	case errors.Is(err, domain.ErrUnauthenticated):
		response.Unauthenticated(writer, requestID)
	case errors.Is(err, domain.ErrUserNotFound):
		response.Error(writer, http.StatusForbidden, requestID, response.APIError{
			Code:    "USER_NOT_REGISTERED",
			Message: "Tu cuenta no está habilitada en la plataforma.",
		})
	case errors.Is(err, domain.ErrUserInactive):
		response.Error(writer, http.StatusForbidden, requestID, response.APIError{
			Code:    "USER_INACTIVE",
			Message: "Tu cuenta se encuentra inactiva.",
		})
	default:
		logger.Error("identity request failed",
			"component", "identity",
			"request_id", requestID,
			"error_type", "internal",
		)
		response.InternalError(writer, requestID)
	}
}

type profileResponse struct {
	ID            string                 `json:"id"`
	Email         string                 `json:"email"`
	DisplayName   string                 `json:"displayName"`
	PhoneNumber   string                 `json:"phoneNumber,omitempty"`
	PlatformRole  domain.PlatformRole    `json:"platformRole"`
	Organizations []organizationResponse `json:"organizations"`
	// OnboardingSeenAt es null mientras la persona no haya terminado u omitido la introducción.
	OnboardingSeenAt *string `json:"onboardingSeenAt"`
}

type organizationResponse struct {
	ID        string                `json:"id"`
	LegalName string                `json:"legalName"`
	TradeName string                `json:"tradeName,omitempty"`
	Role      domain.MembershipRole `json:"role"`
	Locations []locationResponse    `json:"locations"`
}

type locationResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Address          string `json:"address"`
	AddressReference string `json:"addressReference,omitempty"`
	District         string `json:"district,omitempty"`
	Province         string `json:"province,omitempty"`
	Department       string `json:"department,omitempty"`
	Timezone         string `json:"timezone"`
}

func newProfileResponse(profile domain.Profile) profileResponse {
	result := profileResponse{
		ID:               profile.ID,
		Email:            profile.Email,
		DisplayName:      profile.DisplayName,
		PhoneNumber:      profile.PhoneNumber,
		PlatformRole:     profile.PlatformRole,
		Organizations:    make([]organizationResponse, 0, len(profile.Organizations)),
		OnboardingSeenAt: formatSeenAt(profile.OnboardingSeenAt),
	}
	for _, membership := range profile.Organizations {
		organization := organizationResponse{
			ID:        membership.Organization.ID,
			LegalName: membership.Organization.LegalName,
			TradeName: membership.Organization.TradeName,
			Role:      membership.Role,
			Locations: make([]locationResponse, 0, len(membership.Locations)),
		}
		for _, location := range membership.Locations {
			organization.Locations = append(organization.Locations, locationResponse{
				ID:               location.ID,
				Name:             location.Name,
				Address:          location.Address,
				AddressReference: location.AddressReference,
				District:         location.District,
				Province:         location.Province,
				Department:       location.Department,
				Timezone:         location.Timezone,
			})
		}
		result.Organizations = append(result.Organizations, organization)
	}
	return result
}
