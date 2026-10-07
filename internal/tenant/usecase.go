package tenant

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type TenantUsecase struct {
	repo    TenantRepository
	cleanup CleanupAvatars
}

func NewTenantUsecase(repo TenantRepository, cleanup CleanupAvatars) *TenantUsecase {
	return &TenantUsecase{repo: repo, cleanup: cleanup}
}

// SetupTenants validates all accounts before hashing or changing persistence.
func (u *TenantUsecase) SetupTenants(ctx context.Context, tenants []Tenant, getenv func(string) string) error {
	if len(tenants) < 1 || len(tenants) > 100 {
		return errs.BadRequest("provide between 1 and 100 candidate tenants")
	}
	emails := map[string]bool{}
	passwords := make([][2]string, len(tenants))
	for i := range tenants {
		for j, a := range []*Account{&tenants[i].Admin, &tenants[i].Employee} {
			password, err := validateAccount(a, emails, getenv)
			if err != nil {
				return err
			}
			passwords[i][j] = password
		}
	}
	prepared := make([]PreparedTenant, len(tenants))
	for i := range tenants {
		id, err := u.tenantID(ctx, tenants[i].Admin.Email)
		if err != nil {
			return err
		}
		tenants[i].ID = id
		if prepared[i], err = prepareTenant(tenants[i], passwords[i]); err != nil {
			return err
		}
	}
	return u.repo.SetupTenants(ctx, prepared)
}

// tenantID reuses the existing Admin's tenant so repeated setup is stable.
func (u *TenantUsecase) tenantID(ctx context.Context, adminEmail string) (string, error) {
	id, err := u.repo.TenantIDByAdminEmail(ctx, adminEmail)
	if err != nil || id != "" {
		return id, err
	}
	return uuid.NewString(), nil
}

// validateAccount normalizes a, records its email in seen, and returns its password.
func validateAccount(a *Account, seen map[string]bool, getenv func(string) string) (string, error) {
	a.Email = strings.ToLower(strings.TrimSpace(a.Email))
	a.FullName = strings.TrimSpace(a.FullName)
	parsed, err := mail.ParseAddress(a.Email)
	if err != nil || parsed.Address != a.Email || len(a.Email) > 254 || seen[a.Email] {
		return "", errs.BadRequest("provide distinct valid emails for every account")
	}
	seen[a.Email] = true
	if n := len([]rune(a.FullName)); n < 2 || n > 100 {
		return "", errs.BadRequest("names must contain 2-100 characters")
	}
	if a.PasswordEnv == "" {
		return "", errs.BadRequest("passwordEnv is required")
	}
	password := getenv(a.PasswordEnv)
	if n := len(password); n < 8 || n > 72 {
		return "", errs.BadRequest("password environment values must contain 8-72 bytes")
	}
	return password, nil
}

// prepareTenant hashes the Admin and Employee passwords and builds the initial Employee.
func prepareTenant(t Tenant, passwords [2]string) (PreparedTenant, error) {
	a, err := bcrypt.GenerateFromPassword([]byte(passwords[0]), bcrypt.DefaultCost)
	if err != nil {
		return PreparedTenant{}, fmt.Errorf("hash Admin password: %w", err)
	}
	e, err := bcrypt.GenerateFromPassword([]byte(passwords[1]), bcrypt.DefaultCost)
	if err != nil {
		return PreparedTenant{}, fmt.Errorf("hash Employee password: %w", err)
	}
	return PreparedTenant{
		Tenant: t, AdminHash: string(a), EmployeeHash: string(e), DepartmentName: "IT",
		EmployeeProfile: employee.Employee{
			TenantID: t.ID, EmployeeCode: "EMP-0001", FullName: t.Employee.FullName,
			Email: t.Employee.Email, Status: employee.StatusActive, Version: 1,
		},
	}, nil
}

// Reset requires stopped API replicas. Revocation commits before storage cleanup,
// so failed cleanup never restores access. Database records remain for a retry.
func (u *TenantUsecase) Reset(ctx context.Context, tenantID string) error {
	if err := validTenantID(tenantID); err != nil {
		return err
	}
	if u.cleanup == nil {
		return errs.BadRequest("private avatar storage is required for reset")
	}
	if err := u.repo.Revoke(ctx, tenantID); err != nil {
		return err
	}
	if err := u.cleanup(ctx, tenantID); err != nil {
		return fmt.Errorf("avatar cleanup failed; sessions remain revoked; retry reset: %w", err)
	}
	return u.repo.Clear(ctx, tenantID)
}

// AdminEmail identifies the tenant for operator confirmation before Reset.
func (u *TenantUsecase) AdminEmail(ctx context.Context, tenantID string) (string, error) {
	if err := validTenantID(tenantID); err != nil {
		return "", err
	}
	return u.repo.AdminEmail(ctx, tenantID)
}

// ClearAll requires stopped API replicas. Database rows go first so access ends
// even if storage cleanup fails; rerunning retries the cleanup.
func (u *TenantUsecase) ClearAll(ctx context.Context) error {
	if u.cleanup == nil {
		return errs.BadRequest("private avatar storage is required for clear")
	}
	if err := u.repo.ClearAll(ctx); err != nil {
		return err
	}
	if err := u.cleanup(ctx, ""); err != nil {
		return fmt.Errorf("database cleared but avatar cleanup failed; retry db-clear: %w", err)
	}
	return nil
}

func validTenantID(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return errs.BadRequest("tenant ID must be a canonical nonzero UUID")
	}
	return nil
}
