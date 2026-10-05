package project

import (
	"context"
	"errors"
	"slices"
	"time"
)

type TaskType string

const (
	TaskFeature TaskType = "feature"
	TaskBug     TaskType = "bug"
)

// TaskTypes are the task type lookup values, in display order.
var TaskTypes = []TaskType{TaskFeature, TaskBug}

func (t TaskType) Valid() bool { return slices.Contains(TaskTypes, t) }

type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityMedium   Priority = "medium"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

// Priorities are the priority lookup values, lowest first.
var Priorities = []Priority{PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical}

func (p Priority) Valid() bool { return slices.Contains(Priorities, p) }

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
	// TotalPhases and TotalTasks are computed by List only; they are not stored.
	TotalPhases int `gorm:"->;-:migration"`
	TotalTasks  int `gorm:"->;-:migration"`
}

func (Project) TableName() string { return "projects" }

type Phase struct {
	ID        string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	ProjectID string    `gorm:"type:uuid;not null;index"`
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

// ListFilter selects one page of a tenant's Projects; Search matches name or code.
type ListFilter struct {
	TenantID string
	Search   string
	Offset   int
	Limit    int
}

type ProjectRepository interface {
	// List returns one page of tenantID's Projects, newest first, without Phases but with
	// TotalPhases and TotalTasks, plus the total number of matches.
	List(ctx context.Context, filter ListFilter) ([]*Project, int64, error)
	// FindByID returns NOT_FOUND unless the Project exists in tenantID. Phases are sorted
	// by Order; Tasks keep their submitted order.
	FindByID(ctx context.Context, tenantID, id string) (*Project, error)
	// FindByIdempotencyKey is FindByID by key, but returns nil when tenantID has not used key.
	FindByIdempotencyKey(ctx context.Context, tenantID, key string) (*Project, error)
	// ActiveEmployeeIDs returns which ids are active, non-deleted Employees in tenantID.
	ActiveEmployeeIDs(ctx context.Context, tenantID string, ids []string) (map[string]bool, error)
	// Create stores the Project with its Phases and Tasks in one transaction. It returns
	// errKeyUsed when the tenant already used project.IdempotencyKey, and CONFLICT when the
	// tenant already has a Project with the code.
	Create(ctx context.Context, project *Project) error
	// Delete removes the Project with its Phases and Tasks, returning NOT_FOUND unless it
	// exists in tenantID.
	Delete(ctx context.Context, tenantID, id string) error
}
