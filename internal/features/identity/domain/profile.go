package domain

import "time"

type UserStatus string

const (
	UserStatusActive   UserStatus = "ACTIVE"
	UserStatusInactive UserStatus = "INACTIVE"
)

type PlatformRole string

const (
	PlatformRoleUser       PlatformRole = "USER"
	PlatformRoleSuperAdmin PlatformRole = "SUPER_ADMIN"
)

type MembershipRole string

const (
	MembershipRoleAdmin  MembershipRole = "ADMIN"
	MembershipRoleChef   MembershipRole = "CHEF"
	MembershipRoleWorker MembershipRole = "WORKER"
)

type Profile struct {
	ID            string
	Email         string
	DisplayName   string
	PhoneNumber   string
	PlatformRole  PlatformRole
	Status        UserStatus
	Organizations []OrganizationMembership
	// OnboardingSeenAt indica cuándo la persona terminó u omitió la introducción; nil si aún no la vio.
	OnboardingSeenAt *time.Time
}

func (profile Profile) IsActive() bool {
	return profile.Status == UserStatusActive
}

type OrganizationMembership struct {
	Organization Organization
	Role         MembershipRole
	Locations    []Location
}

type Organization struct {
	ID        string
	LegalName string
	TradeName string
}

type Location struct {
	ID               string
	Name             string
	Address          string
	AddressReference string
	District         string
	Province         string
	Department       string
	Timezone         string
}
