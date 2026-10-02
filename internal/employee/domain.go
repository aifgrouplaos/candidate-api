package employee

import (
	"context"
	"time"

	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"gorm.io/gorm"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusOnLeave  Status = "on_leave"
)

func (s Status) Valid() bool {
	return s == StatusActive || s == StatusInactive || s == StatusOnLeave
}

// Department is a shared lookup value, not owned by any candidate tenant.
type Department struct {
	ID   string `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name string `json:"name" gorm:"not null;uniqueIndex"`
}

func (Department) TableName() string { return "departments" }

// Employee is a tenant profile; UserID links its login account and is nil for
// sample rows without one.
type Employee struct {
	ID           string  `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID     string  `gorm:"type:uuid;not null;uniqueIndex:idx_employees_tenant_code"`
	UserID       *string `gorm:"type:uuid;uniqueIndex"`
	EmployeeCode string  `gorm:"not null;uniqueIndex:idx_employees_tenant_code"`
	FullName     string  `gorm:"not null"`
	Email        string  `gorm:"not null;uniqueIndex:idx_employees_email_lower,expression:LOWER(email)"`
	Phone        *string
	DepartmentID *string     `gorm:"type:uuid;index"`
	Department   *Department `gorm:"foreignKey:DepartmentID"`
	Position     *string
	Status       Status     `gorm:"not null;default:active"`
	HireDate     *time.Time `gorm:"type:date"`
	// AvatarURL stores the private object key. Responses replace it with a
	// short-lived presigned URL after the caller is authorized.
	AvatarURL *string
	Version   int            `gorm:"not null;default:1"`
	CreatedAt time.Time      `gorm:"autoCreateTime"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (Employee) TableName() string { return "employees" }

type SortColumn string

const (
	SortFullName  SortColumn = "full_name"
	SortHireDate  SortColumn = "hire_date"
	SortCreatedAt SortColumn = "created_at"
)

type ListFilter struct {
	TenantID     string
	Search       string
	DepartmentID string
	Statuses     []Status
	SortBy       SortColumn
	Desc         bool
	Offset       int
	Limit        int
}

type EmployeeRepository interface {
	List(ctx context.Context, filter ListFilter) ([]*Employee, int64, error)
	// FindByID returns NOT_FOUND unless the Employee exists, is not deleted, and belongs to tenantID.
	FindByID(ctx context.Context, tenantID, id string) (*Employee, error)
	DepartmentExists(ctx context.Context, id string) (bool, error)
	// Create stores the login and Employee atomically, assigns EmployeeCode, and returns
	// CONFLICT for a duplicate email or when the tenant already has maxLogins logins.
	// A Deleted Employee in this tenant with the same email is restored: profile fields and
	// password come from the request, while id, code, avatar, and created time stay.
	Create(ctx context.Context, employee *Employee, login *auth.User, maxLogins int) error
	// Update returns VERSION_CONFLICT unless the stored version equals expectedVersion.
	Update(ctx context.Context, employee *Employee, expectedVersion int) error
	// Delete soft-deletes the Employee, disables its login, and revokes its sessions.
	Delete(ctx context.Context, tenantID, id string, now time.Time) error
	Departments(ctx context.Context) ([]Department, error)
}
