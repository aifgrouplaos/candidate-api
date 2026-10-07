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

type Tenant struct {
	ID       string  `json:"id"`
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
	// SetupTenants atomically creates missing accounts and initial conversations.
	// Existing accounts must match; their passwords are unchanged.
	SetupTenants(context.Context, []PreparedTenant) error
	// Revoke requires one existing Admin and revokes every tenant session.
	Revoke(context.Context, string) error
	// Clear removes assessment records and Employee logins, retaining the Admin.
	Clear(context.Context, string) error
}

// CleanupAvatars removes all objects under exactly avatars/<tenant UUID>/,
// including orphaned uploads, and reports listing and deletion failures.
type CleanupAvatars func(context.Context, string) error
