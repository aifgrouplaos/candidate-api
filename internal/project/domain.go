package project

import (
	"context"
	"errors"
	"time"
)

type TaskType string

const (
	TaskFeature TaskType = "feature"
	TaskBug     TaskType = "bug"
)

func (t TaskType) Valid() bool { return t == TaskFeature || t == TaskBug }

type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityMedium   Priority = "medium"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

func (p Priority) Valid() bool {
	return p == PriorityLow || p == PriorityMedium || p == PriorityHigh || p == PriorityCritical
}

type Severity string

const (
	SeverityMinor    Severity = "minor"
	SeverityMajor    Severity = "major"
	SeverityCritical Severity = "critical"
)

func (s Severity) Valid() bool {
	return s == SeverityMinor || s == SeverityMajor || s == SeverityCritical
}

// Project is a tenant record; OwnerID references an Employee in the same tenant.
// IdempotencyKey and RequestHash record the POST /projects request that created it.
type Project struct {
	ID             string `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID       string `gorm:"type:uuid;not null;uniqueIndex:idx_projects_tenant_code;uniqueIndex:idx_projects_tenant_idempotency_key"`
	Code           string `gorm:"not null;uniqueIndex:idx_projects_tenant_code"`
	IdempotencyKey string `gorm:"not null;uniqueIndex:idx_projects_tenant_idempotency_key"`
	RequestHash    string `gorm:"not null"`
	Name           string `gorm:"not null"`
	Description    *string
	OwnerID        string    `gorm:"type:uuid;not null;index"`
	StartDate      time.Time `gorm:"type:date;not null"`
	EndDate        time.Time `gorm:"type:date;not null"`
	Phases         []Phase   `gorm:"constraint:OnDelete:CASCADE"`
	Version        int       `gorm:"not null;default:1"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

func (Project) TableName() string { return "projects" }

// Phase keeps Position, its index in the submitted array, so reads return the submitted order.
type Phase struct {
	ID        string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	ProjectID string    `gorm:"type:uuid;not null;index"`
	Position  int       `gorm:"not null"`
	Order     int       `gorm:"column:sort_order;not null"`
	Name      string    `gorm:"not null"`
	StartDate time.Time `gorm:"type:date;not null"`
	EndDate   time.Time `gorm:"type:date;not null"`
	Tasks     []Task    `gorm:"constraint:OnDelete:CASCADE"`
}

func (Phase) TableName() string { return "project_phases" }

// Task keeps Position, its index in the submitted array, so reads return the submitted order.
type Task struct {
	ID            string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	PhaseID       string    `gorm:"type:uuid;not null;index"`
	Position      int       `gorm:"not null"`
	Title         string    `gorm:"not null"`
	Type          TaskType  `gorm:"not null"`
	Priority      Priority  `gorm:"not null"`
	AssigneeID    string    `gorm:"type:uuid;not null;index"`
	EstimateHours float64   `gorm:"type:double precision;not null"`
	DueDate       time.Time `gorm:"type:date;not null"`
	Severity      *Severity
}

func (Task) TableName() string { return "project_tasks" }

// errKeyUsed means another Project in the tenant already has the Idempotency-Key.
var errKeyUsed = errors.New("idempotency key already used")

type ProjectRepository interface {
	// FindByID returns NOT_FOUND unless the Project exists in tenantID. Phases and Tasks
	// keep their submitted order.
	FindByID(ctx context.Context, tenantID, id string) (*Project, error)
	// FindByIdempotencyKey is FindByID by key, but returns nil when tenantID has not used key.
	FindByIdempotencyKey(ctx context.Context, tenantID, key string) (*Project, error)
	// ActiveEmployeeIDs returns which ids are active, non-deleted Employees in tenantID.
	ActiveEmployeeIDs(ctx context.Context, tenantID string, ids []string) (map[string]bool, error)
	// Create stores the Project with its Phases and Tasks in one transaction. It returns
	// errKeyUsed when the tenant already used project.IdempotencyKey, and CONFLICT when the
	// tenant already has a Project with the code.
	Create(ctx context.Context, project *Project) error
}
