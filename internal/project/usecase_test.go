package project

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
)

const (
	tenantA   = "aaaaaaaa-0000-0000-0000-000000000000"
	tenantB   = "bbbbbbbb-0000-0000-0000-000000000000"
	ownerA    = "aaaaaaaa-0000-0000-0000-000000000001"
	assigneeA = "aaaaaaaa-0000-0000-0000-000000000002"
	inactiveA = "aaaaaaaa-0000-0000-0000-000000000003"
	employeeB = "bbbbbbbb-0000-0000-0000-000000000001"
)

var (
	adminA    = auth.Principal{UserID: "admin-a", TenantID: tenantA, Role: auth.RoleAdmin}
	adminB    = auth.Principal{UserID: "admin-b", TenantID: tenantB, Role: auth.RoleAdmin}
	employeeA = auth.Principal{UserID: "user-e1", TenantID: tenantA, Role: auth.RoleEmployee}
)

type memoryRepository struct {
	projects map[string]*Project
	active   map[string]string // employee ID -> tenant ID
	// keyRace makes the next Create act as if a concurrent request stored this Project first.
	keyRace *Project
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		projects: map[string]*Project{},
		active:   map[string]string{ownerA: tenantA, assigneeA: tenantA, employeeB: tenantB},
	}
}

func (r *memoryRepository) FindByID(_ context.Context, tenantID, id string) (*Project, error) {
	p, ok := r.projects[id]
	if !ok || p.TenantID != tenantID {
		return nil, errs.NotFound("Project not found.")
	}
	return p, nil
}

func (r *memoryRepository) FindByIdempotencyKey(_ context.Context, tenantID, key string) (*Project, error) {
	for _, p := range r.projects {
		if p.TenantID == tenantID && p.IdempotencyKey == key {
			return p, nil
		}
	}
	return nil, nil
}

func (r *memoryRepository) ActiveEmployeeIDs(_ context.Context, tenantID string, ids []string) (map[string]bool, error) {
	result := map[string]bool{}
	for _, id := range ids {
		if r.active[id] == tenantID {
			result[id] = true
		}
	}
	return result, nil
}

func (r *memoryRepository) Create(_ context.Context, p *Project) error {
	if r.keyRace != nil {
		r.projects[r.keyRace.ID], r.keyRace = r.keyRace, nil
	}
	if used, _ := r.FindByIdempotencyKey(context.Background(), p.TenantID, p.IdempotencyKey); used != nil {
		return errKeyUsed
	}
	p.ID = fmt.Sprintf("p%d", len(r.projects)+1)
	p.Version = 1
	for i := range p.Phases {
		p.Phases[i].ID = fmt.Sprintf("%s-ph%d", p.ID, i)
		for j := range p.Phases[i].Tasks {
			p.Phases[i].Tasks[j].ID = fmt.Sprintf("%s-ph%d-t%d", p.ID, i, j)
		}
	}
	r.projects[p.ID] = p
	return nil
}

func ptr[T any](v T) *T { return &v }

func validTask() TaskInput {
	return TaskInput{Title: "Interview users", Type: TaskFeature, Priority: PriorityHigh, AssigneeID: assigneeA, EstimateHours: ptr(16.0), DueDate: "2026-11-20"}
}

func validInput() CreateProjectInput {
	return CreateProjectInput{
		Name: "HR system upgrade", Code: "PRJ-2026-001", Description: ptr("Project description"), OwnerID: ownerA,
		StartDate: "2026-11-01", EndDate: "2027-03-31",
		Phases: []PhaseInput{{
			Name: "Requirements", Order: ptr(1), StartDate: "2026-11-01", EndDate: "2026-12-15",
			Tasks: []TaskInput{validTask(), {
				Title: "Fix login", Type: TaskBug, Priority: PriorityCritical, AssigneeID: assigneeA,
				EstimateHours: ptr(2.5), DueDate: "2026-12-15", Severity: ptr(SeverityMajor),
			}},
		}},
	}
}

func errorCode(err error) string {
	if appErr, ok := errs.IsAppError(err); ok {
		return appErr.Code
	}
	return ""
}

func detailFields(t *testing.T, err error) []string {
	t.Helper()
	appErr, ok := errs.IsAppError(err)
	if !ok || appErr.Code != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
	var fields []string
	for _, detail := range appErr.Data.([]apierror.FieldError) {
		fields = append(fields, detail.Field)
	}
	return fields
}

func TestCreateReturnsNestedProject(t *testing.T) {
	u := NewProjectUsecase(newMemoryRepository())
	got, err := u.Create(context.Background(), adminA, "key-1", validInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.OwnerID != ownerA || got.StartDate != "2026-11-01" || got.Version != 1 || len(got.Phases) != 1 {
		t.Fatalf("project = %+v", got)
	}
	phase := got.Phases[0]
	if phase.ID == "" || phase.Order != 1 || phase.EndDate != "2026-12-15" || len(phase.Tasks) != 2 {
		t.Fatalf("phase = %+v", phase)
	}
	feature, bug := phase.Tasks[0], phase.Tasks[1]
	if feature.ID == "" || feature.Severity != nil || feature.EstimateHours != 16 || feature.DueDate != "2026-11-20" {
		t.Fatalf("feature task = %+v", feature)
	}
	if bug.Severity == nil || *bug.Severity != SeverityMajor || bug.Type != TaskBug {
		t.Fatalf("bug task = %+v", bug)
	}
}

func TestCreateReportsEveryInvalidField(t *testing.T) {
	input := CreateProjectInput{
		Name: "HR", Code: "", OwnerID: inactiveA, StartDate: "2026-11-01", EndDate: "2027-03-31",
		Phases: []PhaseInput{
			{
				Name: "", Order: ptr(1), StartDate: "2026-10-01", EndDate: "2026-11-30",
				Tasks: []TaskInput{
					validTask(),
					{Title: "Bug without severity", Type: TaskBug, Priority: PriorityLow, AssigneeID: assigneeA, EstimateHours: ptr(1.0), DueDate: "2026-11-02"},
					{Title: "", Type: "chore", Priority: "urgent", AssigneeID: employeeB, EstimateHours: ptr(0.0), DueDate: "2026-12-01", Severity: ptr(SeverityMinor)},
					{Title: "Bad values", Type: TaskBug, Priority: PriorityLow, AssigneeID: "not-a-uuid", EstimateHours: ptr(1000.0), DueDate: "11/20/2026", Severity: ptr(Severity("huge"))},
				},
			},
			{Name: "Empty", Order: ptr(1), StartDate: "2026-11-02", EndDate: "2026-11-01", Tasks: nil},
		},
	}
	_, err := NewProjectUsecase(newMemoryRepository()).Create(context.Background(), adminA, "key-1", input)
	want := []string{
		"name", "code",
		"phases[0].name", "phases[0].startDate",
		"phases[0].tasks[1].severity",
		"phases[0].tasks[2].title", "phases[0].tasks[2].type", "phases[0].tasks[2].priority",
		"phases[0].tasks[2].estimateHours", "phases[0].tasks[2].dueDate",
		"phases[0].tasks[3].assigneeId", "phases[0].tasks[3].estimateHours", "phases[0].tasks[3].dueDate", "phases[0].tasks[3].severity",
		"phases[1].order", "phases[1].endDate", "phases[1].tasks",
		"ownerId", "phases[0].tasks[2].assigneeId",
	}
	if got := detailFields(t, err); !reflect.DeepEqual(got, want) {
		t.Fatalf("fields\n got %v\nwant %v", got, want)
	}
}

func TestCreateBoundsPhaseAndTaskCounts(t *testing.T) {
	u := NewProjectUsecase(newMemoryRepository())
	none := validInput()
	none.Phases = nil
	_, err := u.Create(context.Background(), adminA, "k1", none)
	if got := detailFields(t, err); !reflect.DeepEqual(got, []string{"phases"}) {
		t.Fatalf("no phases: %v", got)
	}

	tooMany := validInput()
	for len(tooMany.Phases) <= maxPhases {
		phase := tooMany.Phases[0]
		phase.Order = ptr(len(tooMany.Phases) + 1)
		tooMany.Phases = append(tooMany.Phases, phase)
	}
	tooMany.Phases[0].Tasks = make([]TaskInput, maxTasksPerPhase+1)
	for i := range tooMany.Phases[0].Tasks {
		tooMany.Phases[0].Tasks[i] = validTask()
	}
	_, err = u.Create(context.Background(), adminA, "k2", tooMany)
	if got := detailFields(t, err); !reflect.DeepEqual(got, []string{"phases", "phases[0].tasks"}) {
		t.Fatalf("too many: %v", got)
	}
}

func TestCreateAccepts500Tasks(t *testing.T) {
	input := validInput()
	input.Phases = nil
	for i := 0; i < 5; i++ {
		phase := PhaseInput{Name: fmt.Sprintf("Phase %d", i), Order: ptr(i + 1), StartDate: "2026-11-01", EndDate: "2026-12-15"}
		for j := 0; j < maxTasksPerPhase; j++ {
			phase.Tasks = append(phase.Tasks, validTask())
		}
		input.Phases = append(input.Phases, phase)
	}
	got, err := NewProjectUsecase(newMemoryRepository()).Create(context.Background(), adminA, "key", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Phases) != 5 || len(got.Phases[4].Tasks) != maxTasksPerPhase {
		t.Fatalf("phases = %d", len(got.Phases))
	}
}

func TestCreateRequiresAdminAndIdempotencyKey(t *testing.T) {
	repo := newMemoryRepository()
	u := NewProjectUsecase(repo)
	if _, err := u.Create(context.Background(), employeeA, "key", validInput()); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("employee create: %v", err)
	}
	if _, err := u.Create(context.Background(), auth.Principal{}, "key", validInput()); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("zero principal create: %v", err)
	}
	for _, key := range []string{"", "   ", string(make([]byte, maxKeyLength+1))} {
		if _, err := u.Create(context.Background(), adminA, key, validInput()); errorCode(err) != "BAD_REQUEST" {
			t.Fatalf("key %q: %v", key, err)
		}
	}
	if len(repo.projects) != 0 {
		t.Fatalf("stored %d projects", len(repo.projects))
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	repo := newMemoryRepository()
	u := NewProjectUsecase(repo)
	ctx := context.Background()
	first, err := u.Create(ctx, adminA, "retry", validInput())
	if err != nil {
		t.Fatal(err)
	}

	again, err := u.Create(ctx, adminA, "retry", validInput())
	if err != nil || again.ID != first.ID || len(repo.projects) != 1 {
		t.Fatalf("same payload: id %v vs %v, err %v, stored %d", again, first.ID, err, len(repo.projects))
	}

	changed := validInput()
	changed.Name = "Another name"
	if _, err := u.Create(ctx, adminA, "retry", changed); errorCode(err) != apierror.IdempotencyConflict.Code {
		t.Fatalf("different payload: %v", err)
	}

	other, err := u.Create(ctx, adminB, "retry", tenantBInput())
	if err != nil || other.ID == first.ID {
		t.Fatalf("keys are per tenant: %v, %v", other, err)
	}
}

func TestCreateReplaysKeyStoredByConcurrentRequest(t *testing.T) {
	ctx := context.Background()
	winner := newMemoryRepository()
	original, err := NewProjectUsecase(winner).Create(ctx, adminA, "race", validInput())
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepository()
	repo.keyRace = winner.projects[original.ID]

	got, err := NewProjectUsecase(repo).Create(ctx, adminA, "race", validInput())
	if err != nil || got.ID != original.ID || len(repo.projects) != 1 {
		t.Fatalf("got %v, err %v, stored %d", got, err, len(repo.projects))
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	repo := newMemoryRepository()
	u := NewProjectUsecase(repo)
	ctx := context.Background()
	created, err := u.Create(ctx, adminA, "key", validInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []auth.Principal{adminA, employeeA} {
		got, err := u.Get(ctx, actor, created.ID)
		if err != nil || got.ID != created.ID || len(got.Phases[0].Tasks) != 2 {
			t.Fatalf("%s get: %v, %v", actor.Role, got, err)
		}
	}
	if _, err := u.Get(ctx, adminB, created.ID); errorCode(err) != "NOT_FOUND" {
		t.Fatalf("other tenant: %v", err)
	}
	if _, err := u.Get(ctx, auth.Principal{}, created.ID); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("zero principal: %v", err)
	}
}

func tenantBInput() CreateProjectInput {
	input := validInput()
	input.OwnerID = employeeB
	for i := range input.Phases[0].Tasks {
		input.Phases[0].Tasks[i].AssigneeID = employeeB
	}
	return input
}
