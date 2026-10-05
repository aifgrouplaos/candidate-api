package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
)

// newTestDB needs TEST_POSTGRES_DSN in key=value form, for example
// "host=localhost port=5435 user=postgres password=secret dbname=candidate-api_db sslmode=disable".
func newTestDB(t *testing.T) *gormadapter.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	admin, err := gormadapter.New(config.Postgres{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("project_test_%d", time.Now().UnixNano())
	if err := admin.Raw().Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Raw().Exec("DROP SCHEMA " + schema + " CASCADE")
		admin.Close()
	})
	db, err := gormadapter.New(config.Postgres{DSN: dsn + " search_path=" + schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Raw().AutoMigrate(&employee.Department{}, &employee.Employee{}, &Project{}, &Phase{}, &Task{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedEmployee(t *testing.T, db *gormadapter.DB, id, tenantID string, status employee.Status, deleted bool) {
	t.Helper()
	e := &employee.Employee{ID: id, TenantID: tenantID, EmployeeCode: "EMP-" + id[len(id)-4:], FullName: "Seed", Email: id + "@example.test", Status: status}
	if err := db.Raw().Create(e).Error; err != nil {
		t.Fatal(err)
	}
	if deleted {
		if err := db.Raw().Delete(e).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// storedProject builds a Project with phases × tasksPerPhase Tasks, all assigned to ownerA.
func storedProject(code, key string, phases, tasksPerPhase int) *Project {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	p := &Project{TenantID: tenantA, Code: code, IdempotencyKey: key, RequestHash: "h", Name: "Project " + code, OwnerID: ownerA, StartDate: day(1), EndDate: day(30)}
	for i := 0; i < phases; i++ {
		phase := Phase{Position: i, Order: phases - i, Name: fmt.Sprintf("Phase %d", i), StartDate: day(1), EndDate: day(30)}
		for j := 0; j < tasksPerPhase; j++ {
			phase.Tasks = append(phase.Tasks, Task{
				Position: j, Title: fmt.Sprintf("Task %d.%d", i, j), Type: TaskBug, Priority: PriorityLow,
				AssigneeID: ownerA, EstimateHours: 1.5, DueDate: day(2), Severity: ptr(SeverityMinor),
			})
		}
		p.Phases = append(p.Phases, phase)
	}
	return p
}

func rowCounts(t *testing.T, db *gormadapter.DB) [3]int64 {
	t.Helper()
	var counts [3]int64
	for i, model := range []any{&Project{}, &Phase{}, &Task{}} {
		if err := db.Raw().Model(model).Count(&counts[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

type repoFixture struct {
	db      *gormadapter.DB
	repo    ProjectRepository
	ctx     context.Context
	project *Project
}

func TestRepositoryAgainstPostgres(t *testing.T) {
	db := newTestDB(t)
	f := &repoFixture{db: db, repo: NewProjectRepository(db), ctx: context.Background()}
	seedEmployee(t, db, ownerA, tenantA, employee.StatusActive, false)
	seedEmployee(t, db, assigneeA, tenantA, employee.StatusActive, true)
	seedEmployee(t, db, inactiveA, tenantA, employee.StatusOnLeave, false)
	seedEmployee(t, db, employeeB, tenantB, employee.StatusActive, false)

	t.Run("active employees exclude deleted, inactive, and other tenants", f.testActiveEmployees)

	f.project = storedProject("PRJ-1", "k1", 5, 100)
	if err := f.repo.Create(f.ctx, f.project); err != nil {
		t.Fatal(err)
	}

	t.Run("reads back 500 tasks in submitted order", f.testReadBackOrder)
	t.Run("other tenants and malformed IDs are not found", f.testNotFound)
	t.Run("finds the project by key per tenant", f.testFindByKey)
	t.Run("reused key stores nothing", f.testReusedKey)
	t.Run("duplicate code conflicts only within the tenant", f.testDuplicateCode)
	t.Run("a failing task rolls back the whole submission", f.testRollback)
	t.Run("concurrent submissions with one key create one project", f.testConcurrentKey)
}

func (f *repoFixture) testActiveEmployees(t *testing.T) {
	active, err := f.repo.ActiveEmployeeIDs(f.ctx, tenantA, []string{ownerA, assigneeA, inactiveA, employeeB})
	if err != nil || len(active) != 1 || !active[ownerA] {
		t.Fatalf("active = %v, err %v", active, err)
	}
}

func (f *repoFixture) testReadBackOrder(t *testing.T) {
	got, err := f.repo.FindByID(f.ctx, tenantA, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Phases) != 5 || got.Phases[0].Order != 5 || got.Phases[4].Name != "Phase 4" {
		t.Fatalf("project = %+v", got)
	}
	tasks := got.Phases[4].Tasks
	if len(tasks) != 100 || tasks[99].Title != "Task 4.99" || tasks[0].ID == "" || *tasks[0].Severity != SeverityMinor || !tasks[0].DueDate.Equal(f.project.Phases[0].Tasks[0].DueDate) {
		t.Fatalf("tasks = %d, last %+v", len(tasks), tasks[len(tasks)-1])
	}
}

func (f *repoFixture) testNotFound(t *testing.T) {
	for _, c := range []struct{ tenant, id string }{{tenantB, f.project.ID}, {tenantA, "not-a-uuid"}, {tenantA, employeeB}} {
		if _, err := f.repo.FindByID(f.ctx, c.tenant, c.id); errorCode(err) != "NOT_FOUND" {
			t.Fatalf("%v: %v", c, err)
		}
	}
}

func (f *repoFixture) testFindByKey(t *testing.T) {
	found, err := f.repo.FindByIdempotencyKey(f.ctx, tenantA, "k1")
	if err != nil || found == nil || found.ID != f.project.ID || found.RequestHash != "h" || len(found.Phases[4].Tasks) != 100 {
		t.Fatalf("found = %+v, err %v", found, err)
	}
	if found, err := f.repo.FindByIdempotencyKey(f.ctx, tenantB, "k1"); found != nil || err != nil {
		t.Fatalf("other tenant = %+v, err %v", found, err)
	}
}

func (f *repoFixture) testReusedKey(t *testing.T) {
	before := rowCounts(t, f.db)
	err := f.repo.Create(f.ctx, storedProject("PRJ-2", "k1", 1, 1))
	if !errors.Is(err, errKeyUsed) || rowCounts(t, f.db) != before {
		t.Fatalf("err %v, counts %v -> %v", err, before, rowCounts(t, f.db))
	}
}

func (f *repoFixture) testDuplicateCode(t *testing.T) {
	err := f.repo.Create(f.ctx, storedProject("PRJ-1", "k2", 1, 1))
	if errorCode(err) != "CONFLICT" {
		t.Fatalf("same tenant: %v", err)
	}
	other := storedProject("PRJ-1", "k2", 1, 1)
	other.TenantID = tenantB
	if err := f.repo.Create(f.ctx, other); err != nil {
		t.Fatalf("other tenant: %v", err)
	}
}

func (f *repoFixture) testRollback(t *testing.T) {
	before := rowCounts(t, f.db)
	broken := storedProject("PRJ-ATOMIC", "atomic", 5, 100)
	broken.Phases[4].Tasks[99].AssigneeID = "not-a-uuid"
	err := f.repo.Create(f.ctx, broken)
	if err == nil || rowCounts(t, f.db) != before {
		t.Fatalf("err %v, counts %v -> %v", err, before, rowCounts(t, f.db))
	}
}

func (f *repoFixture) testConcurrentKey(t *testing.T) {
	before := rowCounts(t, f.db)
	var wg sync.WaitGroup
	results := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = f.repo.Create(f.ctx, storedProject("PRJ-RACE", "race", 1, 1))
		}(i)
	}
	wg.Wait()
	created := 0
	for _, err := range results {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, errKeyUsed):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if after := rowCounts(t, f.db); created != 1 || after[0] != before[0]+1 {
		t.Fatalf("created %d, counts %v -> %v", created, before, after)
	}
}
