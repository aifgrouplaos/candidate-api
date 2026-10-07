package assessment

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

type AssessmentUsecase struct {
	repo    AssessmentRepository
	cleanup CleanupAvatars
}

func NewAssessmentUsecase(repo AssessmentRepository, cleanup CleanupAvatars) *AssessmentUsecase {
	return &AssessmentUsecase{repo: repo, cleanup: cleanup}
}

// SetupTenants validates all accounts before hashing or changing persistence.
func (u *AssessmentUsecase) SetupTenants(ctx context.Context, tenants []Tenant, getenv func(string) string) error {
	if len(tenants) < 1 || len(tenants) > 100 {
		return errs.BadRequest("provide between 1 and 100 candidate tenants")
	}
	ids, emails := map[string]bool{}, map[string]bool{}
	passwords := make([][2]string, len(tenants))
	for i := range tenants {
		t := &tenants[i]
		if err := validTenantID(t.ID); err != nil {
			return err
		}
		if ids[t.ID] {
			return errs.BadRequest("tenant IDs must be distinct")
		}
		ids[t.ID] = true
		for j, a := range []*Account{&t.Admin, &t.Employee} {
			a.Email = strings.ToLower(strings.TrimSpace(a.Email))
			a.FullName = strings.TrimSpace(a.FullName)
			parsed, err := mail.ParseAddress(a.Email)
			if err != nil || parsed.Address != a.Email || len(a.Email) > 254 || emails[a.Email] {
				return errs.BadRequest("provide distinct valid emails for every account")
			}
			emails[a.Email] = true
			if n := len([]rune(a.FullName)); n < 2 || n > 100 {
				return errs.BadRequest("names must contain 2–100 characters")
			}
			if a.PasswordEnv == "" {
				return errs.BadRequest("passwordEnv is required")
			}
			passwords[i][j] = getenv(a.PasswordEnv)
			if n := len(passwords[i][j]); n < 8 || n > 72 {
				return errs.BadRequest("password environment values must contain 8–72 bytes")
			}
		}
	}
	prepared := make([]PreparedTenant, len(tenants))
	for i, t := range tenants {
		a, err := bcrypt.GenerateFromPassword([]byte(passwords[i][0]), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash Admin password: %w", err)
		}
		e, err := bcrypt.GenerateFromPassword([]byte(passwords[i][1]), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash Employee password: %w", err)
		}
		prepared[i] = PreparedTenant{
			Tenant: t, AdminHash: string(a), EmployeeHash: string(e), DepartmentName: "IT",
			EmployeeProfile: employee.Employee{
				TenantID: t.ID, EmployeeCode: "EMP-0001", FullName: t.Employee.FullName,
				Email: t.Employee.Email, Status: employee.StatusActive, Version: 1,
			},
		}
	}
	return u.repo.SetupTenants(ctx, prepared)
}

// Reset requires stopped API replicas. Revocation commits before storage cleanup,
// so failed cleanup never restores access. Database records remain for a retry.
func (u *AssessmentUsecase) Reset(ctx context.Context, tenantID string) error {
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

func validTenantID(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return errs.BadRequest("tenant ID must be a canonical nonzero UUID")
	}
	return nil
}
