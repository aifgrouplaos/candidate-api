package employee

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"golang.org/x/crypto/bcrypt"
)

const (
	departmentIT = "11111111-1111-1111-1111-111111111111"
	tenantA      = "aaaaaaaa-0000-0000-0000-000000000000"
	tenantB      = "bbbbbbbb-0000-0000-0000-000000000000"
)

var (
	adminA    = auth.Principal{UserID: "admin-a", TenantID: tenantA, Role: auth.RoleAdmin}
	adminB    = auth.Principal{UserID: "admin-b", TenantID: tenantB, Role: auth.RoleAdmin}
	employeeA = auth.Principal{UserID: "user-e1", TenantID: tenantA, Role: auth.RoleEmployee}
)

type memoryRepository struct {
	employees  map[string]*Employee
	logins     map[string]*auth.User
	lastFilter ListFilter
	deletedAt  map[string]time.Time
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		employees: map[string]*Employee{
			"e1": {ID: "e1", TenantID: tenantA, UserID: ptr("user-e1"), EmployeeCode: "EMP-0001", FullName: "Somchai", Email: "somchai@example.test", Status: StatusActive, Version: 1},
			"e2": {ID: "e2", TenantID: tenantA, UserID: ptr("user-e2"), EmployeeCode: "EMP-0002", FullName: "Other", Email: "other@example.test", Status: StatusActive, Version: 1},
		},
		logins:    map[string]*auth.User{},
		deletedAt: map[string]time.Time{},
	}
}

func (r *memoryRepository) List(_ context.Context, filter ListFilter) ([]*Employee, int64, error) {
	r.lastFilter = filter
	var result []*Employee
	for _, e := range r.employees {
		if e.TenantID == filter.TenantID {
			result = append(result, e)
		}
	}
	return result, int64(len(result)), nil
}

func (r *memoryRepository) FindByID(_ context.Context, tenantID, id string) (*Employee, error) {
	e, ok := r.employees[id]
	if !ok || e.TenantID != tenantID {
		return nil, errs.NotFound("Employee not found.")
	}
	copied := *e
	return &copied, nil
}

func (r *memoryRepository) DepartmentExists(_ context.Context, id string) (bool, error) {
	return id == departmentIT, nil
}

func (r *memoryRepository) Create(_ context.Context, e *Employee, login *auth.User, _ int) error {
	login.ID = "new-user"
	e.ID, e.UserID, e.EmployeeCode, e.Version = "new", &login.ID, "EMP-0003", 1
	r.logins[login.ID] = login
	r.employees[e.ID] = e
	return nil
}

func (r *memoryRepository) Update(_ context.Context, e *Employee, expectedVersion int) error {
	if r.employees[e.ID].Version != expectedVersion {
		return apierror.VersionConflict
	}
	e.Version++
	r.employees[e.ID] = e
	return nil
}

func (r *memoryRepository) Delete(_ context.Context, tenantID, id string, now time.Time) error {
	if _, err := r.FindByID(context.Background(), tenantID, id); err != nil {
		return err
	}
	r.deletedAt[id] = now
	delete(r.employees, id)
	return nil
}

func (r *memoryRepository) Departments(context.Context) ([]Department, error) {
	return []Department{{ID: departmentIT, Name: "IT"}}, nil
}

func errorCode(err error) string {
	if appErr, ok := errs.IsAppError(err); ok {
		return appErr.Code
	}
	return ""
}

func detailFields(t *testing.T, err error) map[string]bool {
	t.Helper()
	appErr, ok := errs.IsAppError(err)
	if !ok || appErr.Code != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
	fields := map[string]bool{}
	for _, detail := range appErr.Data.([]apierror.FieldError) {
		fields[detail.Field] = true
	}
	return fields
}

func ptr[T any](value T) *T { return &value }

func patch(body string) UpdateEmployeeInput {
	var input UpdateEmployeeInput
	if err := json.Unmarshal([]byte(body), &input); err != nil {
		panic(err)
	}
	return input
}

func TestCreateMakesEmployeeLoginInCallerTenant(t *testing.T) {
	repo := newMemoryRepository()
	uc := NewEmployeeUsecase(repo)
	created, err := uc.Create(context.Background(), adminA, CreateEmployeeInput{
		FullName: " Somsak Example ", Email: "SOMSAK@example.test", Password: "password-123",
		Phone: ptr("020 5555 5555"), DepartmentID: ptr(departmentIT), Position: ptr("Developer"), HireDate: ptr("2024-03-01"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	login := repo.logins["new-user"]
	if login.Role != auth.RoleEmployee || login.TenantID != tenantA || !login.Active || login.Email != "somsak@example.test" {
		t.Fatalf("unexpected login %+v", login)
	}
	if bcrypt.CompareHashAndPassword([]byte(login.PasswordHash), []byte("password-123")) != nil {
		t.Fatal("password was not stored as a bcrypt hash")
	}
	if created.FullName != "Somsak Example" || created.Status != StatusActive || *created.HireDate != "2024-03-01" || created.EmployeeCode != "EMP-0003" {
		t.Fatalf("unexpected view %+v", created)
	}
}

func TestCreateReportsEveryInvalidField(t *testing.T) {
	uc := NewEmployeeUsecase(newMemoryRepository())
	_, err := uc.Create(context.Background(), adminA, CreateEmployeeInput{
		FullName: "A", Email: "not-an-email", Password: "ກຂຄ", Phone: ptr("call me"),
		DepartmentID: ptr("22222222-2222-2222-2222-222222222222"), Position: ptr(string(make([]byte, 101))),
		Status: "fired", HireDate: ptr(time.Now().UTC().AddDate(0, 0, 2).Format(time.DateOnly)),
	})
	fields := detailFields(t, err)
	for _, field := range []string{"fullName", "email", "password", "phone", "departmentId", "position", "status", "hireDate"} {
		if !fields[field] {
			t.Errorf("missing validation detail for %s in %v", field, fields)
		}
	}
}

func TestOnlyAdminsListCreateAndDelete(t *testing.T) {
	uc := NewEmployeeUsecase(newMemoryRepository())
	ctx := context.Background()
	if _, _, err := uc.List(ctx, employeeA, ListQuery{}); errorCode(err) != "FORBIDDEN" {
		t.Errorf("employee list: got %v", err)
	}
	if _, err := uc.Create(ctx, employeeA, CreateEmployeeInput{}); errorCode(err) != "FORBIDDEN" {
		t.Errorf("employee create: got %v", err)
	}
	if err := uc.Delete(ctx, employeeA, "e1"); errorCode(err) != "FORBIDDEN" {
		t.Errorf("employee delete: got %v", err)
	}
}

func TestOtherTenantAdminCannotSeeOrChangeEmployee(t *testing.T) {
	repo := newMemoryRepository()
	uc := NewEmployeeUsecase(repo)
	ctx := context.Background()
	if _, err := uc.Get(ctx, adminB, "e1"); errorCode(err) != "NOT_FOUND" {
		t.Errorf("get: got %v", err)
	}
	if _, err := uc.Update(ctx, adminB, "e1", patch(`{"version":1,"fullName":"Hijacked"}`)); errorCode(err) != "NOT_FOUND" {
		t.Errorf("update: got %v", err)
	}
	if err := uc.Delete(ctx, adminB, "e1"); errorCode(err) != "NOT_FOUND" {
		t.Errorf("delete: got %v", err)
	}
	if _, _, err := uc.List(ctx, adminB, ListQuery{}); err != nil || repo.lastFilter.TenantID != tenantB {
		t.Errorf("list must be scoped to the caller tenant, filter %+v err %v", repo.lastFilter, err)
	}
	if repo.employees["e1"].FullName != "Somchai" {
		t.Fatal("cross-tenant update changed the Employee")
	}
}

func TestEmployeeReadsOnlyOwnProfile(t *testing.T) {
	uc := NewEmployeeUsecase(newMemoryRepository())
	ctx := context.Background()
	if self, err := uc.Get(ctx, employeeA, "e1"); err != nil || self.ID != "e1" {
		t.Fatalf("self read: %v %v", self, err)
	}
	for _, id := range []string{"e2", "missing"} {
		if _, err := uc.Get(ctx, employeeA, id); errorCode(err) != "FORBIDDEN" {
			t.Errorf("read %s: got %v", id, err)
		}
	}
}

func TestPatchEnforcesRoleWritableFields(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		actor auth.Principal
		id    string
		body  string
		code  string
	}{
		{"employee updates own profile fields", employeeA, "e1", `{"version":1,"fullName":"New Name","phone":null,"avatarUrl":"https://cdn.example.test/a.png"}`, ""},
		{"employee cannot change status", employeeA, "e1", `{"version":1,"status":"inactive"}`, "FORBIDDEN"},
		{"employee cannot change email", employeeA, "e1", `{"version":1,"email":"x@example.test"}`, "FORBIDDEN"},
		{"employee cannot patch another employee", employeeA, "e2", `{"version":1,"fullName":"Other Name"}`, "FORBIDDEN"},
		{"admin updates administrative fields", adminA, "e2", `{"version":1,"status":"on_leave","departmentId":"` + departmentIT + `","hireDate":"2024-01-31","position":null}`, ""},
		{"admin cannot set avatarUrl", adminA, "e2", `{"version":1,"avatarUrl":null}`, "FORBIDDEN"},
		{"nobody changes role", adminA, "e2", `{"version":1,"role":"admin"}`, "FORBIDDEN"},
		{"version is required", adminA, "e2", `{"fullName":"Valid Name"}`, "VALIDATION_ERROR"},
		{"stale version conflicts", adminA, "e2", `{"version":2,"fullName":"Valid Name"}`, "VERSION_CONFLICT"},
		{"invalid employee values are rejected", employeeA, "e1", `{"version":1,"fullName":null,"avatarUrl":"javascript:alert(1)"}`, "VALIDATION_ERROR"},
		{"avatarUrl must be https", employeeA, "e1", `{"version":1,"avatarUrl":"http://cdn.example.test/a.png"}`, "VALIDATION_ERROR"},
		{"invalid admin values are rejected", adminA, "e2", `{"version":1,"fullName":"","email":"bad","status":"gone"}`, "VALIDATION_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository()
			updated, err := NewEmployeeUsecase(repo).Update(ctx, tc.actor, tc.id, patch(tc.body))
			if errorCode(err) != tc.code {
				t.Fatalf("got error %v, want code %q", err, tc.code)
			}
			if tc.code != "" {
				if repo.employees[tc.id].Version != 1 {
					t.Fatal("rejected patch was persisted")
				}
				return
			}
			if updated.Version != 2 {
				t.Fatalf("version = %d, want 2", updated.Version)
			}
		})
	}
}

func TestPatchAppliesOnlyProvidedFields(t *testing.T) {
	repo := newMemoryRepository()
	repo.employees["e1"].Phone = ptr("020 1111 1111")
	repo.employees["e1"].Position = ptr("Developer")
	updated, err := NewEmployeeUsecase(repo).Update(context.Background(), employeeA, "e1", patch(`{"version":1,"phone":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Phone != nil || updated.Position == nil || *updated.Position != "Developer" || updated.FullName != "Somchai" {
		t.Fatalf("unexpected patch result %+v", updated)
	}
}

func TestListNormalizesQuery(t *testing.T) {
	repo := newMemoryRepository()
	uc := NewEmployeeUsecase(repo)
	_, meta, err := uc.List(context.Background(), adminA, ListQuery{
		Page: 0, Limit: 500, Search: "  Som  ", DepartmentID: departmentIT,
		Status: "active, on_leave,active", SortBy: "fullName", SortOrder: "desc",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ListFilter{TenantID: tenantA, Search: "Som", DepartmentID: departmentIT, Statuses: []Status{StatusActive, StatusOnLeave}, SortBy: SortFullName, Desc: true, Offset: 0, Limit: 100}
	got := repo.lastFilter
	if got.TenantID != want.TenantID || got.Search != want.Search || got.DepartmentID != want.DepartmentID || got.SortBy != want.SortBy ||
		got.Desc != want.Desc || got.Offset != want.Offset || got.Limit != want.Limit || len(got.Statuses) != 2 || got.Statuses[1] != StatusOnLeave {
		t.Fatalf("filter = %+v, want %+v", got, want)
	}
	if meta.Page != 1 || meta.Limit != 100 || meta.Total != 2 || meta.TotalPages != 1 {
		t.Fatalf("meta = %+v", meta)
	}
	if _, _, err := uc.List(context.Background(), adminA, ListQuery{Page: 3, Limit: 10}); err != nil || repo.lastFilter.Offset != 20 || repo.lastFilter.SortBy != SortCreatedAt {
		t.Fatalf("defaults: filter %+v err %v", repo.lastFilter, err)
	}
	_, _, err = uc.List(context.Background(), adminA, ListQuery{Status: "active,fired", SortBy: "password", SortOrder: "up", DepartmentID: "IT"})
	fields := detailFields(t, err)
	for _, field := range []string{"status", "sortBy", "sortOrder", "departmentId"} {
		if !fields[field] {
			t.Errorf("missing validation detail for %s", field)
		}
	}
}

func TestDeleteRemovesTenantEmployee(t *testing.T) {
	repo := newMemoryRepository()
	if err := NewEmployeeUsecase(repo).Delete(context.Background(), adminA, "e1"); err != nil {
		t.Fatal(err)
	}
	if _, deleted := repo.deletedAt["e1"]; !deleted {
		t.Fatal("repository delete was not called")
	}
}
