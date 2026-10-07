package employee

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"gorm.io/gorm"
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
	schema := fmt.Sprintf("employee_test_%d", time.Now().UnixNano())
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
	if err := db.Raw().AutoMigrate(&auth.User{}, &auth.AuthSession{}, &auth.RefreshToken{}, &Department{}, &Employee{}); err != nil {
		t.Fatal(err)
	}
	if err := SeedDepartments(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := SeedDepartments(context.Background(), db); err != nil {
		t.Fatalf("seeding twice must be idempotent: %v", err)
	}
	return db
}

func createEmployee(t *testing.T, repo EmployeeRepository, tenantID, name, email string, departmentID *string, status Status) *Employee {
	t.Helper()
	e := &Employee{TenantID: tenantID, FullName: name, Email: email, DepartmentID: departmentID, Status: status}
	login := &auth.User{TenantID: tenantID, Email: email, PasswordHash: "hash", Role: auth.RoleEmployee, FullName: name, Active: true}
	if err := repo.Create(context.Background(), e, login, maxTenantLogins); err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return e
}

func TestCreateRollsBackWhenProvisionFails(t *testing.T) {
	db := newTestDB(t)
	failed := errors.New("provision failed")
	var provisioned string
	repo := NewEmployeeRepository(db, func(_ *gorm.DB, tenantID, employeeID string) error {
		provisioned = tenantID + "/" + employeeID
		return failed
	})
	e := &Employee{TenantID: tenantA, FullName: "Rollback", Email: "rollback@example.test", Status: StatusActive}
	login := &auth.User{TenantID: tenantA, Email: e.Email, PasswordHash: "hash", Role: auth.RoleEmployee, FullName: e.FullName, Active: true}
	if err := repo.Create(context.Background(), e, login, maxTenantLogins); !errors.Is(err, failed) {
		t.Fatalf("err = %v", err)
	}
	if provisioned != tenantA+"/"+e.ID {
		t.Fatalf("provisioned = %q", provisioned)
	}
	var employees, users int64
	db.Raw().Unscoped().Model(&Employee{}).Count(&employees)
	db.Raw().Model(&auth.User{}).Count(&users)
	if employees != 0 || users != 0 {
		t.Fatalf("employees = %d, users = %d after rollback", employees, users)
	}
}

func TestRepositoryAgainstPostgres(t *testing.T) {
	db := newTestDB(t)
	repo := NewEmployeeRepository(db, nil)
	ctx := context.Background()

	departments, err := repo.Departments(ctx)
	if err != nil || len(departments) != len(defaultDepartments) {
		t.Fatalf("departments = %v, err %v", departments, err)
	}
	it := &departments[0].ID

	first := createEmployee(t, repo, tenantA, "ສົມຊາຍ Vongsa", "somchai@example.test", it, StatusActive)
	second := createEmployee(t, repo, tenantA, "Anna 100% Real", "anna@example.test", nil, StatusOnLeave)
	createEmployee(t, repo, tenantA, "Zed Inactive", "zed@example.test", nil, StatusInactive)
	createEmployee(t, repo, tenantB, "Other Tenant", "other@example.test", nil, StatusActive)
	if first.EmployeeCode != "EMP-0001" || second.EmployeeCode != "EMP-0002" {
		t.Fatalf("codes = %s, %s", first.EmployeeCode, second.EmployeeCode)
	}

	t.Run("duplicate email conflicts case-insensitively", func(t *testing.T) {
		err := repo.Create(ctx, &Employee{TenantID: tenantB, FullName: "Dup", Email: "dup@example.test", Status: StatusActive},
			&auth.User{TenantID: tenantB, Email: "SOMCHAI@example.test", PasswordHash: "hash", Role: auth.RoleEmployee, FullName: "Dup", Active: true}, maxTenantLogins)
		if errorCode(err) != "CONFLICT" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("tenant login limit", func(t *testing.T) {
		err := repo.Create(ctx, &Employee{TenantID: tenantA, FullName: "Over", Email: "over@example.test", Status: StatusActive},
			&auth.User{TenantID: tenantA, Email: "over@example.test", PasswordHash: "hash", Role: auth.RoleEmployee, FullName: "Over", Active: true}, 3)
		if errorCode(err) != "CONFLICT" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("list filters, sorts, and stays in tenant", func(t *testing.T) {
		names := func(filter ListFilter) ([]string, int64) {
			t.Helper()
			filter.TenantID, filter.Limit = tenantA, 10
			if filter.SortBy == "" {
				filter.SortBy = SortCreatedAt
			}
			list, total, err := repo.List(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			result := make([]string, len(list))
			for i, e := range list {
				result[i] = e.FullName
			}
			return result, total
		}
		cases := []struct {
			name   string
			filter ListFilter
			want   []string
		}{
			{"all in creation order", ListFilter{}, []string{"ສົມຊາຍ Vongsa", "Anna 100% Real", "Zed Inactive"}},
			{"search Lao name", ListFilter{Search: "ສົມ"}, []string{"ສົມຊາຍ Vongsa"}},
			{"search code", ListFilter{Search: "emp-0002"}, []string{"Anna 100% Real"}},
			{"percent is literal", ListFilter{Search: "%"}, []string{"Anna 100% Real"}},
			{"department", ListFilter{DepartmentID: *it}, []string{"ສົມຊາຍ Vongsa"}},
			{"statuses", ListFilter{Statuses: []Status{StatusInactive, StatusOnLeave}, SortBy: SortFullName}, []string{"Anna 100% Real", "Zed Inactive"}},
			{"name desc", ListFilter{SortBy: SortFullName, Desc: true}, []string{"ສົມຊາຍ Vongsa", "Zed Inactive", "Anna 100% Real"}},
		}
		for _, tc := range cases {
			got, total := names(tc.filter)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) || total != int64(len(tc.want)) {
				t.Errorf("%s: got %v (total %d), want %v", tc.name, got, total, tc.want)
			}
		}
		page, total := names(ListFilter{Offset: 2})
		if total != 3 || len(page) != 1 {
			t.Errorf("offset page = %v, total %d", page, total)
		}
		if _, err := repo.FindByID(ctx, tenantB, first.ID); errorCode(err) != "NOT_FOUND" {
			t.Errorf("cross-tenant find: %v", err)
		}
	})

	t.Run("update checks version and syncs login", func(t *testing.T) {
		loaded, err := repo.FindByID(ctx, tenantA, second.ID)
		if err != nil {
			t.Fatal(err)
		}
		loaded.Email, loaded.FullName = "anna.new@example.test", "Anna New"
		if err := repo.Update(ctx, loaded, 1); err != nil {
			t.Fatal(err)
		}
		if err := repo.Update(ctx, loaded, 1); errorCode(err) != "VERSION_CONFLICT" {
			t.Fatalf("stale update: %v", err)
		}
		reloaded, _ := repo.FindByID(ctx, tenantA, second.ID)
		if reloaded.Version != 2 || reloaded.FullName != "Anna New" || !reloaded.UpdatedAt.After(reloaded.CreatedAt) {
			t.Fatalf("reloaded = %+v", reloaded)
		}
		var login auth.User
		if err := db.Session(ctx).First(&login, "id = ?", *reloaded.UserID).Error; err != nil || login.Email != "anna.new@example.test" || login.FullName != "Anna New" {
			t.Fatalf("login not synced: %+v %v", login, err)
		}
		reloaded.Email = "somchai@example.test"
		if err := repo.Update(ctx, reloaded, 2); errorCode(err) != "CONFLICT" {
			t.Fatalf("duplicate email update: %v", err)
		}
	})

	t.Run("delete disables login and revokes sessions", func(t *testing.T) {
		avatar := "https://cdn.example/avatar.png"
		position := "Old role"
		first.AvatarURL = &avatar
		first.Position = &position
		if err := repo.Update(ctx, first, 1); err != nil {
			t.Fatal(err)
		}
		authRepo := auth.NewAuthRepository(db)
		session := &auth.AuthSession{ID: "99999999-9999-9999-9999-999999999999", UserID: *first.UserID, TenantID: tenantA}
		if err := authRepo.CreateSession(ctx, session, &auth.RefreshToken{TokenHash: "h1", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if id, err := authRepo.EmployeeID(ctx, *first.UserID, tenantA); err != nil || id == nil || *id != first.ID {
			t.Fatalf("employee id = %v, err %v", id, err)
		}
		if id, err := authRepo.EmployeeID(ctx, *first.UserID, tenantB); err != nil || id != nil {
			t.Fatalf("cross-tenant employee id = %v, err %v", id, err)
		}
		if err := repo.Delete(ctx, tenantB, first.ID, time.Now()); errorCode(err) != "NOT_FOUND" {
			t.Fatalf("cross-tenant delete: %v", err)
		}
		if err := repo.Delete(ctx, tenantA, first.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.FindByID(ctx, tenantA, first.ID); errorCode(err) != "NOT_FOUND" {
			t.Fatalf("deleted employee still visible: %v", err)
		}
		if active, err := authRepo.SessionActive(ctx, session.ID, session.UserID, tenantA); err != nil || active {
			t.Fatalf("session still active: %v %v", active, err)
		}
		if id, err := authRepo.EmployeeID(ctx, *first.UserID, tenantA); err != nil || id != nil {
			t.Fatalf("deleted employee id = %v, err %v", id, err)
		}
		if _, err := authRepo.FindActiveUserByEmail(ctx, "somchai@example.test"); errorCode(err) != "UNAUTHORIZED" {
			t.Fatalf("deleted employee can still log in: %v", err)
		}
		if _, _, err := authRepo.FindUserByRefreshToken(ctx, "h1", time.Now()); errorCode(err) != "UNAUTHORIZED" {
			t.Fatalf("refresh token still usable: %v", err)
		}
		var retained int64
		db.Session(ctx).Unscoped().Model(&Employee{}).Where("id = ?", first.ID).Count(&retained)
		if retained != 1 {
			t.Fatal("soft delete removed the Employee record")
		}
		if err := repo.Delete(ctx, tenantA, first.ID, time.Now()); errorCode(err) != "NOT_FOUND" {
			t.Fatalf("second delete: %v", err)
		}
	})

	t.Run("create restores a deleted employee in the same tenant", func(t *testing.T) {
		authRepo := auth.NewAuthRepository(db)
		hireDate := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
		phone := "020555555"
		incoming := &Employee{
			TenantID: tenantA, FullName: "Somchai Restored", Email: "somchai@example.test",
			Phone: &phone, DepartmentID: it, Status: StatusOnLeave, HireDate: &hireDate,
		}
		login := &auth.User{
			TenantID: tenantA, Email: incoming.Email, PasswordHash: "new-hash",
			Role: auth.RoleEmployee, FullName: incoming.FullName, Active: true,
		}
		if err := repo.Create(ctx, &Employee{TenantID: tenantB, FullName: "Taken", Email: incoming.Email, Status: StatusActive},
			&auth.User{TenantID: tenantB, Email: incoming.Email, PasswordHash: "hash", Role: auth.RoleEmployee, FullName: "Taken", Active: true}, maxTenantLogins); errorCode(err) != "CONFLICT" {
			t.Fatalf("other tenant took a deleted email: %v", err)
		}
		if err := repo.Create(ctx, incoming, login, 2); errorCode(err) != "CONFLICT" {
			t.Fatalf("restore ignored the login limit: %v", err)
		}
		if err := repo.Create(ctx, incoming, login, maxTenantLogins); err != nil {
			t.Fatal(err)
		}
		if incoming.ID != first.ID || incoming.EmployeeCode != "EMP-0001" {
			t.Fatalf("restored identity = %s %s", incoming.ID, incoming.EmployeeCode)
		}
		restored, err := repo.FindByID(ctx, tenantA, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if restored.FullName != "Somchai Restored" || restored.Email != "somchai@example.test" || restored.Status != StatusOnLeave || restored.Version != 3 {
			t.Fatalf("restored profile = %+v", restored)
		}
		if restored.Phone == nil || *restored.Phone != phone || restored.DepartmentID == nil || *restored.DepartmentID != *it || restored.Position != nil {
			t.Fatalf("restored fields = %+v", restored)
		}
		if restored.HireDate == nil || restored.HireDate.Format(time.DateOnly) != "2020-01-02" {
			t.Fatalf("hire date = %v", restored.HireDate)
		}
		// PostgreSQL persists microseconds; the in-memory creation time can be finer.
		if restored.AvatarURL == nil || *restored.AvatarURL != "https://cdn.example/avatar.png" || !restored.CreatedAt.Equal(first.CreatedAt.Truncate(time.Microsecond)) || !restored.UpdatedAt.After(restored.CreatedAt) {
			t.Fatalf("retained fields = %+v", restored)
		}
		if _, err := authRepo.FindActiveUserByEmail(ctx, "somchai@example.test"); err != nil {
			t.Fatalf("restored employee cannot log in: %v", err)
		}
		if active, err := authRepo.SessionActive(ctx, "99999999-9999-9999-9999-999999999999", *first.UserID, tenantA); err != nil || active {
			t.Fatalf("revoked session was restored: %v %v", active, err)
		}
		var stored auth.User
		if err := db.Session(ctx).First(&stored, "id = ?", *first.UserID).Error; err != nil || stored.PasswordHash != "new-hash" || stored.FullName != "Somchai Restored" || !stored.Active {
			t.Fatalf("login not replaced: %+v %v", stored, err)
		}
		var rows int64
		if err := db.Session(ctx).Unscoped().Model(&Employee{}).Where("LOWER(email) = ?", "somchai@example.test").Count(&rows).Error; err != nil || rows != 1 {
			t.Fatalf("email rows = %d, err %v", rows, err)
		}
	})
}
