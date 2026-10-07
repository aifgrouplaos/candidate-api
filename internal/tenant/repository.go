package tenant

import (
	"context"
	"errors"
	"time"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/internal/chat"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
	"gorm.io/gorm"
)

type tenantRepository struct{ db contract.ORM }

func NewTenantRepository(db contract.ORM) TenantRepository {
	return &tenantRepository{db: db}
}

func (r *tenantRepository) SetupTenants(ctx context.Context, tenants []PreparedTenant) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		for _, t := range tenants {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "employees:"+t.Tenant.ID).Error; err != nil {
				return err
			}
			var admins []auth.User
			if err := tx.Where("tenant_id = ? AND role = ?", t.Tenant.ID, auth.RoleAdmin).Find(&admins).Error; err != nil {
				return err
			}
			if len(admins) > 1 || (len(admins) == 1 && admins[0].Email != t.Tenant.Admin.Email) {
				return errs.Conflict("tenant already has a different Admin")
			}
			if _, err := provisionUser(tx, t.Tenant.ID, t.Tenant.Admin, auth.RoleAdmin, t.AdminHash); err != nil {
				return err
			}
			login, err := provisionUser(tx, t.Tenant.ID, t.Tenant.Employee, auth.RoleEmployee, t.EmployeeHash)
			if err != nil {
				return err
			}
			var e employee.Employee
			err = tx.Unscoped().Where("LOWER(email) = ?", t.Tenant.Employee.Email).First(&e).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				var count int64
				if err := tx.Unscoped().Model(&employee.Employee{}).Where("tenant_id = ?", t.Tenant.ID).Count(&count).Error; err != nil {
					return err
				}
				if count != 0 {
					return errs.Conflict("tenant has Employees but its initial Employee is missing; reset first")
				}
				var department employee.Department
				if err := tx.Where("name = ?", t.DepartmentName).First(&department).Error; err != nil {
					return err
				}
				e = t.EmployeeProfile
				e.UserID = &login.ID
				e.DepartmentID = &department.ID
				if err := tx.Create(&e).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if e.TenantID != t.Tenant.ID || e.UserID == nil || *e.UserID != login.ID || e.DeletedAt.Valid {
				return errs.Conflict("initial Employee does not match; reset first")
			}
			if err := chat.ProvisionConversation(tx, t.Tenant.ID, e.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func provisionUser(tx *gorm.DB, tenantID string, a Account, role auth.Role, hash string) (*auth.User, error) {
	var user auth.User
	err := tx.Where("LOWER(email) = ?", a.Email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user = auth.User{TenantID: tenantID, Email: a.Email, FullName: a.FullName, Role: role, PasswordHash: hash, Active: true}
		return &user, tx.Create(&user).Error
	}
	if err != nil {
		return nil, err
	}
	if user.TenantID != tenantID || user.Role != role || !user.Active {
		return nil, errs.Conflict("account does not match the candidate tenant")
	}
	return &user, nil
}

func (r *tenantRepository) Revoke(ctx context.Context, tenantID string) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&auth.User{}).Where("tenant_id = ? AND role = ?", tenantID, auth.RoleAdmin).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errs.NotFound("candidate tenant must have exactly one Admin")
		}
		now := time.Now().UTC()
		if err := tx.Model(&auth.AuthSession{}).Where("tenant_id = ? AND revoked_at IS NULL", tenantID).Update("revoked_at", now).Error; err != nil {
			return err
		}
		return tx.Model(&auth.RefreshToken{}).Where("user_id IN (SELECT id FROM users WHERE tenant_id = ?) AND revoked_at IS NULL", tenantID).Update("revoked_at", now).Error
	})
}

func (r *tenantRepository) Clear(ctx context.Context, tenantID string) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		// Explicit child deletion works independently of cascade migrations.
		statements := []string{
			"DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE tenant_id = ?)",
			"DELETE FROM conversations WHERE tenant_id = ?",
			"DELETE FROM project_tasks WHERE phase_id IN (SELECT id FROM project_phases WHERE project_id IN (SELECT id FROM projects WHERE tenant_id = ?))",
			"DELETE FROM project_phases WHERE project_id IN (SELECT id FROM projects WHERE tenant_id = ?)",
			"DELETE FROM projects WHERE tenant_id = ?",
			"DELETE FROM employees WHERE tenant_id = ?",
			"DELETE FROM refresh_tokens WHERE user_id IN (SELECT id FROM users WHERE tenant_id = ? AND role = 'employee')",
			"DELETE FROM auth_sessions WHERE tenant_id = ? AND user_id IN (SELECT id FROM users WHERE role = 'employee')",
			"DELETE FROM users WHERE tenant_id = ? AND role = 'employee'",
		}
		for _, sql := range statements {
			if err := tx.Exec(sql, tenantID).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
