package tenant

import (
	"context"

	"github.com/aifgrouplaos/candidate-api/internal/employee"
)

// Account references an environment variable rather than embedding a password.
type Account struct {
	Email       string `json:"email"`
	FullName    string `json:"fullName"`
	PasswordEnv string `json:"passwordEnv"`
}

// Tenant.ID is not read from the manifest; setup reuses the Admin's tenant or generates one.
type Tenant struct {
	ID       string  `json:"-"`
	Admin    Account `json:"admin"`
	Employee Account `json:"employee"`
}

type PreparedTenant struct {
	Tenant          Tenant
	AdminHash       string
	EmployeeHash    string
	EmployeeProfile employee.Employee
	DepartmentName  string
}

type TenantRepository interface {
	// TenantIDByAdminEmail returns the tenant of the Admin with email, or "" if none exists.
	TenantIDByAdminEmail(ctx context.Context, email string) (string, error)
	// SetupTenants atomically creates missing accounts and initial conversations.
	// Existing accounts must match; their passwords are unchanged.
	SetupTenants(context.Context, []PreparedTenant) error
	// AdminEmail returns the email of the tenant's only Admin.
	AdminEmail(context.Context, string) (string, error)
	// Revoke requires one existing Admin and revokes every tenant session.
	Revoke(context.Context, string) error
	// Clear removes every tenant record, including the Admin.
	Clear(context.Context, string) error
	// ClearAll truncates every table in the current schema.
	ClearAll(context.Context) error
}

// CleanupAvatars removes all objects under exactly files/avatars/<tenant UUID>/,
// or under files/avatars/ for an empty ID, including orphaned uploads, and
// reports listing and deletion failures.
type CleanupAvatars func(context.Context, string) error
