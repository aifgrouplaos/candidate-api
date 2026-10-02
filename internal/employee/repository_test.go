package employee

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
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

func TestRepositoryAgainstPostgres(t *testing.T) {
	db := newTestDB(t)
	repo := NewEmployeeRepository(db)
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
		authRepo := auth.NewAuthRepository(db)
		session := &auth.AuthSession{ID: "99999999-9999-9999-9999-999999999999", UserID: *first.UserID, TenantID: tenantA}
		if err := authRepo.CreateSession(ctx, session, &auth.RefreshToken{TokenHash: "h1", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
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
}
