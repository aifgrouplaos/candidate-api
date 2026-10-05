package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/aifgrouplaos/candidate-api/pkg/utils"
	"github.com/google/uuid"
)

const (
	maxPhases        = 20
	maxTasksPerPhase = 100
	maxOrder         = 1000
	maxEstimateHours = 999
	maxKeyLength     = 255
	msgNoEmployee    = "Employee not found."
	msgDate          = "Date must be a YYYY-MM-DD date."
	msgEndBeforeDate = "End date must be on or after the start date."
	msgPhaseDates    = "Phase dates must be within the Project dates."
)

var errForbidden = *errs.Forbidden("You do not have permission to perform this action.")

type CreateProjectInput struct {
	Name        string       `json:"name"`
	Code        string       `json:"code"`
	Description *string      `json:"description"`
	OwnerID     string       `json:"ownerId"`
	StartDate   string       `json:"startDate"`
	EndDate     string       `json:"endDate"`
	Phases      []PhaseInput `json:"phases"`
}

type PhaseInput struct {
	Name      string      `json:"name"`
	Order     *int        `json:"order"`
	StartDate string      `json:"startDate"`
	EndDate   string      `json:"endDate"`
	Tasks     []TaskInput `json:"tasks"`
}

type TaskInput struct {
	Title         string    `json:"title"`
	Type          TaskType  `json:"type"`
	Priority      Priority  `json:"priority"`
	AssigneeID    string    `json:"assigneeId"`
	EstimateHours *float64  `json:"estimateHours"`
	DueDate       string    `json:"dueDate"`
	Severity      *Severity `json:"severity"`
}

type ProjectView struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Code        string      `json:"code"`
	Description *string     `json:"description"`
	OwnerID     string      `json:"ownerId"`
	StartDate   string      `json:"startDate"`
	EndDate     string      `json:"endDate"`
	Phases      []PhaseView `json:"phases"`
	Version     int         `json:"version"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
}

type PhaseView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Order     int        `json:"order"`
	StartDate string     `json:"startDate"`
	EndDate   string     `json:"endDate"`
	Tasks     []TaskView `json:"tasks"`
}

type TaskView struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Type          TaskType  `json:"type"`
	Priority      Priority  `json:"priority"`
	AssigneeID    string    `json:"assigneeId"`
	EstimateHours float64   `json:"estimateHours"`
	DueDate       string    `json:"dueDate"`
	Severity      *Severity `json:"severity"`
}

type ProjectUsecase interface {
	// Create stores a Project hierarchy once per tenant Idempotency-Key. Repeating the key
	// with the same payload returns the original Project.
	Create(ctx context.Context, actor auth.Principal, idempotencyKey string, input CreateProjectInput) (*ProjectView, error)
	Get(ctx context.Context, actor auth.Principal, id string) (*ProjectView, error)
}

type projectUsecase struct {
	repo ProjectRepository
}

func NewProjectUsecase(repo ProjectRepository) ProjectUsecase {
	return &projectUsecase{repo: repo}
}

func (u *projectUsecase) Create(ctx context.Context, actor auth.Principal, idempotencyKey string, input CreateProjectInput) (*ProjectView, error) {
	if actor.Role != auth.RoleAdmin {
		return nil, errForbidden
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || utf8.RuneCountInString(idempotencyKey) > maxKeyLength {
		return nil, errs.BadRequest("The Idempotency-Key header is required and must be at most 255 characters.")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	requestHash := hex.EncodeToString(sum[:])

	// Replay before validating so a retry still returns the original Project after,
	// for example, its owner was deleted.
	if view, err := u.replay(ctx, actor.TenantID, idempotencyKey, requestHash); view != nil || err != nil {
		return view, err
	}
	project, err := u.build(ctx, actor.TenantID, input)
	if err != nil {
		return nil, err
	}
	project.IdempotencyKey, project.RequestHash = idempotencyKey, requestHash
	err = u.repo.Create(ctx, project)
	if errors.Is(err, errKeyUsed) {
		view, err := u.replay(ctx, actor.TenantID, idempotencyKey, requestHash)
		if view == nil && err == nil {
			err = errs.Conflict("The idempotency key is already in use.")
		}
		return view, err
	}
	if err != nil {
		return nil, err
	}
	return u.Get(ctx, actor, project.ID)
}

// Get lets any authenticated user of the tenant read its Projects.
func (u *projectUsecase) Get(ctx context.Context, actor auth.Principal, id string) (*ProjectView, error) {
	if !actor.Role.Valid() || actor.TenantID == "" {
		return nil, errForbidden
	}
	project, err := u.repo.FindByID(ctx, actor.TenantID, id)
	if err != nil {
		return nil, err
	}
	return view(project), nil
}

// replay returns the Project created with key, or nil when the tenant has not used key.
func (u *projectUsecase) replay(ctx context.Context, tenantID, key, requestHash string) (*ProjectView, error) {
	project, err := u.repo.FindByIdempotencyKey(ctx, tenantID, key)
	if err != nil || project == nil {
		return nil, err
	}
	if project.RequestHash != requestHash {
		return nil, apierror.IdempotencyConflict
	}
	return view(project), nil
}

// build validates input and returns the Project to store, reporting every invalid field.
func (u *projectUsecase) build(ctx context.Context, tenantID string, input CreateProjectInput) (*Project, error) {
	var v validation
	project := &Project{
		TenantID:    tenantID,
		Name:        strings.TrimSpace(input.Name),
		Code:        strings.TrimSpace(input.Code),
		Description: utils.Optional(input.Description),
	}
	v.Length("name", project.Name, 3, 100, "Name must be 3-100 characters.")
	v.Length("code", project.Code, 1, 50, "Code is required and must be at most 50 characters.")
	if project.Description != nil {
		v.Length("description", *project.Description, 0, 2000, "Description must be at most 2000 characters.")
	}
	project.OwnerID = v.employee("ownerId", input.OwnerID)
	project.StartDate, project.EndDate = v.dates("", input.StartDate, input.EndDate)
	if n := len(input.Phases); n < 1 || n > maxPhases {
		v.Add("phases", fmt.Sprintf("A Project must have 1-%d phases.", maxPhases))
	}

	orders := map[int]bool{}
	project.Phases = make([]Phase, len(input.Phases))
	for i, in := range input.Phases {
		v.phase(&project.Phases[i], i, in, project, orders)
	}

	if err := u.checkEmployees(ctx, &v, tenantID); err != nil {
		return nil, err
	}
	return project, v.Err()
}

// phase validates the Phase at index i and its Tasks; orders tracks Orders already used in the Project.
func (v *validation) phase(phase *Phase, i int, in PhaseInput, project *Project, orders map[int]bool) {
	path := fmt.Sprintf("phases[%d].", i)
	phase.Position = i
	phase.Name = strings.TrimSpace(in.Name)
	v.Length(path+"name", phase.Name, 1, 100, "Name is required and must be at most 100 characters.")
	switch {
	case in.Order == nil || *in.Order < 1 || *in.Order > maxOrder:
		v.Add(path+"order", fmt.Sprintf("Order must be 1-%d.", maxOrder))
	case orders[*in.Order]:
		v.Add(path+"order", "Order must be unique within the Project.")
	default:
		phase.Order = *in.Order
		orders[phase.Order] = true
	}
	phase.StartDate, phase.EndDate = v.dates(path, in.StartDate, in.EndDate)
	v.within(path+"startDate", phase.StartDate, project.StartDate, project.EndDate, msgPhaseDates)
	v.within(path+"endDate", phase.EndDate, project.StartDate, project.EndDate, msgPhaseDates)
	if n := len(in.Tasks); n < 1 || n > maxTasksPerPhase {
		v.Add(path+"tasks", fmt.Sprintf("A Phase must have 1-%d tasks.", maxTasksPerPhase))
	}

	phase.Tasks = make([]Task, len(in.Tasks))
	for j, task := range in.Tasks {
		v.task(&phase.Tasks[j], fmt.Sprintf("%stasks[%d].", path, j), j, task, phase)
	}
}

// task validates the Task at index j, whose fields are reported under path.
func (v *validation) task(task *Task, path string, j int, in TaskInput, phase *Phase) {
	task.Position = j
	task.Title = strings.TrimSpace(in.Title)
	v.Length(path+"title", task.Title, 1, 200, "Title is required and must be at most 200 characters.")
	task.Type, task.Priority = in.Type, in.Priority
	if !in.Type.Valid() {
		v.Add(path+"type", "Type must be feature or bug.")
	}
	if !in.Priority.Valid() {
		v.Add(path+"priority", "Priority must be low, medium, high, or critical.")
	}
	task.AssigneeID = v.employee(path+"assigneeId", in.AssigneeID)
	if in.EstimateHours == nil || *in.EstimateHours <= 0 || *in.EstimateHours > maxEstimateHours {
		v.Add(path+"estimateHours", "Estimate hours must be greater than 0 and at most 999.")
	} else {
		task.EstimateHours = *in.EstimateHours
	}
	task.DueDate = v.date(path+"dueDate", in.DueDate)
	v.within(path+"dueDate", task.DueDate, phase.StartDate, phase.EndDate, "Due date must be within the Phase dates.")
	task.Severity = in.Severity
	switch {
	case in.Type == TaskBug && (in.Severity == nil || !in.Severity.Valid()):
		v.Add(path+"severity", "Severity must be minor, major, or critical for bug tasks.")
	case in.Type != TaskBug && in.Severity != nil:
		v.Add(path+"severity", "Severity must be null unless the task is a bug.")
	}
}

// checkEmployees reports every referenced Employee that is not active in the tenant.
// ponytail: an Employee deleted between this check and the insert can still be referenced;
// lock the Employee rows inside the Create transaction if that window matters.
func (u *projectUsecase) checkEmployees(ctx context.Context, v *validation, tenantID string) error {
	if len(v.refs) == 0 {
		return nil
	}
	ids := make([]string, len(v.refs))
	for i, ref := range v.refs {
		ids[i] = ref.id
	}
	active, err := u.repo.ActiveEmployeeIDs(ctx, tenantID, utils.Unique(ids))
	if err != nil {
		return err
	}
	for _, ref := range v.refs {
		if !active[ref.id] {
			v.Add(ref.field, msgNoEmployee)
		}
	}
	return nil
}

func view(p *Project) *ProjectView {
	result := &ProjectView{
		ID: p.ID, Name: p.Name, Code: p.Code, Description: p.Description, OwnerID: p.OwnerID,
		StartDate: p.StartDate.Format(time.DateOnly), EndDate: p.EndDate.Format(time.DateOnly),
		Phases: make([]PhaseView, len(p.Phases)), Version: p.Version,
		CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	}
	for i, phase := range p.Phases {
		tasks := make([]TaskView, len(phase.Tasks))
		for j, t := range phase.Tasks {
			tasks[j] = TaskView{
				ID: t.ID, Title: t.Title, Type: t.Type, Priority: t.Priority, AssigneeID: t.AssigneeID,
				EstimateHours: t.EstimateHours, DueDate: t.DueDate.Format(time.DateOnly), Severity: t.Severity,
			}
		}
		result.Phases[i] = PhaseView{
			ID: phase.ID, Name: phase.Name, Order: phase.Order,
			StartDate: phase.StartDate.Format(time.DateOnly), EndDate: phase.EndDate.Format(time.DateOnly), Tasks: tasks,
		}
	}
	return result
}

type employeeRef struct{ id, field string }

type validation struct {
	apierror.FieldErrors
	// refs are well-formed Employee IDs to check against the tenant after field validation.
	refs []employeeRef
}

// employee returns the canonical ID and queues it for the tenant check.
func (v *validation) employee(field, value string) string {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		v.Add(field, msgNoEmployee)
		return ""
	}
	v.refs = append(v.refs, employeeRef{id: id.String(), field: field})
	return id.String()
}

// date returns the zero time for an invalid date, which later range checks skip.
func (v *validation) date(field, value string) time.Time {
	date, err := time.Parse(time.DateOnly, value)
	if err != nil {
		v.Add(field, msgDate)
		return time.Time{}
	}
	return date
}

// dates returns zero times unless both dates are valid and in order, so dependent
// range checks report only the field that is actually wrong.
func (v *validation) dates(path, startValue, endValue string) (time.Time, time.Time) {
	start, end := v.date(path+"startDate", startValue), v.date(path+"endDate", endValue)
	if start.IsZero() || end.IsZero() {
		return time.Time{}, time.Time{}
	}
	if end.Before(start) {
		v.Add(path+"endDate", msgEndBeforeDate)
		return time.Time{}, time.Time{}
	}
	return start, end
}

// within checks that date falls in the inclusive range; it skips unknown dates.
func (v *validation) within(field string, date, start, end time.Time, message string) {
	if date.IsZero() || start.IsZero() || end.IsZero() {
		return
	}
	if date.Before(start) || date.After(end) {
		v.Add(field, message)
	}
}
