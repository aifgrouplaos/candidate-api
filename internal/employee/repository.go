package employee

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	uniqueViolation = "23505"
	whereID         = "id = ?"
)

var (
	errNotFound   = *errs.NotFound("Employee not found.")
	errEmailTaken = *errs.Conflict("The email is already in use.")
)

// defaultDepartments must keep at least five entries.
var defaultDepartments = []string{"IT", "Human Resources", "Finance", "Marketing", "Operations"}

type employeeRepository struct {
	db contract.ORM
}

func NewEmployeeRepository(db contract.ORM) EmployeeRepository {
	return &employeeRepository{db: db}
}

// SeedDepartments inserts defaultDepartments, keeping any that already exist.
func SeedDepartments(ctx context.Context, db contract.ORM) error {
	departments := make([]Department, len(defaultDepartments))
	for i, name := range defaultDepartments {
		departments[i] = Department{Name: name}
	}
	return db.Session(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&departments).Error
}

func (r *employeeRepository) List(ctx context.Context, filter ListFilter) ([]*Employee, int64, error) {
	query := r.db.Session(ctx).Model(&Employee{}).Where("tenant_id = ?", filter.TenantID)
	if filter.Search != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(filter.Search) + "%"
		query = query.Where("(full_name ILIKE ? OR email ILIKE ? OR employee_code ILIKE ?)", pattern, pattern, pattern)
	}
	if filter.DepartmentID != "" {
		query = query.Where("department_id = ?", filter.DepartmentID)
	}
	if len(filter.Statuses) != 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	employees := []*Employee{}
	err := query.Preload("Department").
		Order(clause.OrderByColumn{Column: clause.Column{Name: string(filter.SortBy)}, Desc: filter.Desc}).
		Order("id").Offset(filter.Offset).Limit(filter.Limit).Find(&employees).Error
	return employees, total, err
}

func (r *employeeRepository) FindByID(ctx context.Context, tenantID, id string) (*Employee, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errNotFound
	}
	var employee Employee
	err := r.db.Session(ctx).Preload("Department").Where("id = ? AND tenant_id = ?", id, tenantID).First(&employee).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	return &employee, err
}

func (r *employeeRepository) DepartmentExists(ctx context.Context, id string) (bool, error) {
	var count int64
	err := r.db.Session(ctx).Model(&Department{}).Where(whereID, id).Count(&count).Error
	return count == 1, err
}

func (r *employeeRepository) Create(ctx context.Context, employee *Employee, login *auth.User, maxLogins int) error {
	err := r.db.Transaction(ctx, func(tx *gorm.DB) error {
		// Serializes creations per tenant so the login limit and employee code are race-free.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "employees:"+employee.TenantID).Error; err != nil {
			return err
		}
		var logins int64
		if err := tx.Model(&Employee{}).Where("tenant_id = ? AND user_id IS NOT NULL", employee.TenantID).Count(&logins).Error; err != nil {
			return err
		}
		if logins >= int64(maxLogins) {
			return errs.Conflict(fmt.Sprintf("The tenant already has the maximum of %d Employee logins.", maxLogins))
		}
		var last int
		if err := tx.Unscoped().Model(&Employee{}).
			Where("tenant_id = ? AND employee_code ~ '^EMP-[0-9]+$'", employee.TenantID).
			Select("COALESCE(MAX(SUBSTRING(employee_code FROM 5)::int), 0)").Scan(&last).Error; err != nil {
			return err
		}
		if err := tx.Create(login).Error; err != nil {
			return err
		}
		employee.UserID = &login.ID
		employee.EmployeeCode = fmt.Sprintf("EMP-%04d", last+1)
		employee.Version = 1
		return tx.Omit("Department").Create(employee).Error
	})
	return emailConflict(err)
}

func (r *employeeRepository) Update(ctx context.Context, employee *Employee, expectedVersion int) error {
	err := r.db.Transaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&Employee{}).
			Where("id = ? AND tenant_id = ? AND version = ?", employee.ID, employee.TenantID, expectedVersion).
			Updates(map[string]any{
				"full_name": employee.FullName, "email": employee.Email, "phone": employee.Phone,
				"department_id": employee.DepartmentID, "position": employee.Position, "status": employee.Status,
				"hire_date": employee.HireDate, "avatar_url": employee.AvatarURL, "version": gorm.Expr("version + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apierror.VersionConflict
		}
		if employee.UserID == nil {
			return nil
		}
		return tx.Model(&auth.User{}).Where(whereID, *employee.UserID).
			Updates(map[string]any{"email": employee.Email, "full_name": employee.FullName}).Error
	})
	return emailConflict(err)
}

func (r *employeeRepository) Delete(ctx context.Context, tenantID, id string, now time.Time) error {
	if _, err := uuid.Parse(id); err != nil {
		return errNotFound
	}
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		var employee Employee
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", id, tenantID).First(&employee).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if err := tx.Delete(&employee).Error; err != nil {
			return err
		}
		if employee.UserID == nil {
			return nil
		}
		userID := *employee.UserID
		if err := tx.Model(&auth.User{}).Where(whereID, userID).Update("active", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&auth.AuthSession{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error; err != nil {
			return err
		}
		return tx.Model(&auth.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error
	})
}

func (r *employeeRepository) Departments(ctx context.Context) ([]Department, error) {
	departments := []Department{}
	err := r.db.Session(ctx).Order("name").Order("id").Find(&departments).Error
	return departments, err
}

func emailConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return errEmailTaken
	}
	return err
}
