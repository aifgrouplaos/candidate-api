package tenant_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/internal/chat"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
	"github.com/aifgrouplaos/candidate-api/internal/project"
	"github.com/aifgrouplaos/candidate-api/internal/tenant"
	"gorm.io/gorm"
)

func tenantDB(t *testing.T) *gormadapter.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	admin, err := gormadapter.New(config.Postgres{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("tenant_test_%d", time.Now().UnixNano())
	if err := admin.Raw().Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Raw().Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	db, err := gormadapter.New(config.Postgres{DSN: dsn + " search_path=" + schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Raw().AutoMigrate(&auth.User{}, &auth.AuthSession{}, &auth.RefreshToken{}, &employee.Department{}, &employee.Employee{}, &chat.Conversation{}, &chat.Message{}, &project.Project{}, &project.Phase{}, &project.Task{}); err != nil {
		t.Fatal(err)
	}
	if err := employee.SeedDepartments(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestProvisionAndResetIsolationAgainstPostgres(t *testing.T) {
	db := tenantDB(t)
	ctx := context.Background()
	repo := tenant.NewTenantRepository(db)
	var cleaned []string
	uc := tenant.NewTenantUsecase(repo, func(_ context.Context, id string) error { cleaned = append(cleaned, id); return nil })
	ts := tenants()
	provision := func() {
		t.Helper()
		if err := uc.SetupTenants(ctx, ts, func(string) string { return "test-only-password" }); err != nil {
			t.Fatal(err)
		}
	}
	provision()
	provision()
	// Stable identity and unchanged password hashes across repeated setup.
	var users []auth.User
	if err := db.Raw().Order("email").Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	if len(users) != 4 {
		t.Fatalf("users=%d", len(users))
	}
	hashes := map[string]string{}
	for _, u := range users {
		hashes[u.ID] = u.PasswordHash
	}
	provision()
	if err := db.Raw().Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if hashes[u.ID] != u.PasswordHash {
			t.Fatal("repeat provision changed a password")
		}
	}
	for i, candidate := range ts {
		var e employee.Employee
		var c chat.Conversation
		if err := db.Raw().Where("tenant_id = ?", candidate.ID).First(&e).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Raw().Where("tenant_id = ?", candidate.ID).First(&c).Error; err != nil {
			t.Fatal(err)
		}
		session := auth.AuthSession{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+10), UserID: *e.UserID, TenantID: candidate.ID}
		mustCreate(t, db.Raw(), &session)
		mustCreate(t, db.Raw(), &auth.RefreshToken{UserID: *e.UserID, SessionID: session.ID, TokenHash: fmt.Sprintf("token-%d", i), ExpiresAt: time.Now().Add(time.Hour)})
		var admin auth.User
		if err := db.Raw().Where("tenant_id = ? AND role = ?", candidate.ID, auth.RoleAdmin).First(&admin).Error; err != nil {
			t.Fatal(err)
		}
		adminSession := auth.AuthSession{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+20), UserID: admin.ID, TenantID: candidate.ID}
		mustCreate(t, db.Raw(), &adminSession)
		mustCreate(t, db.Raw(), &auth.RefreshToken{UserID: admin.ID, SessionID: adminSession.ID, TokenHash: fmt.Sprintf("admin-token-%d", i), ExpiresAt: time.Now().Add(time.Hour)})
		mustCreate(t, db.Raw(), &chat.Message{ConversationID: c.ID, SenderID: *e.UserID, Sequence: 1, ClientMessageID: "one", Text: "hello"})
		now := time.Now()
		p := project.Project{TenantID: candidate.ID, Code: "ASSESSMENT", IdempotencyKey: "one", RequestHash: "hash", Name: "Assessment", OwnerID: e.ID, StartDate: now, EndDate: now, Phases: []project.Phase{{Name: "Phase", StartDate: now, EndDate: now, Tasks: []project.Task{{Title: "Task", Type: project.TaskFeature, Priority: project.PriorityLow, AssigneeID: e.ID, EstimateHours: 1, DueDate: now}}}}}
		mustCreate(t, db.Raw(), &p)
		// Include Deleted Employees in reset coverage.
		if i == 0 {
			if err := db.Raw().Delete(&e).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 2 {
		if err := uc.Reset(ctx, ts[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(cleaned) != 2 || cleaned[0] != ts[0].ID || cleaned[1] != ts[0].ID {
		t.Fatalf("cleanup=%v", cleaned)
	}
	for _, table := range []string{"employees", "conversations", "projects"} {
		assertCount(t, db.Raw().Table(table).Where("tenant_id = ?", ts[0].ID), 0)
		assertCount(t, db.Raw().Table(table).Where("tenant_id = ?", ts[1].ID), 1)
	}
	for _, table := range []string{"messages", "project_phases", "project_tasks"} {
		assertCount(t, db.Raw().Table(table), 1)
	}
	assertCount(t, db.Raw().Model(&auth.User{}).Where("tenant_id = ?", ts[0].ID), 1)
	assertCount(t, db.Raw().Model(&auth.AuthSession{}).Where("tenant_id = ? AND revoked_at IS NULL", ts[0].ID), 0)
	assertCount(t, db.Raw().Model(&auth.AuthSession{}).Where("tenant_id = ? AND revoked_at IS NULL", ts[1].ID), 2)
	assertCount(t, db.Raw().Model(&auth.RefreshToken{}).Where("revoked_at IS NULL"), 2)
	assertCount(t, db.Raw().Model(&employee.Department{}), 5)
	provision()
	provision()
	assertCount(t, db.Raw().Model(&employee.Employee{}).Where("tenant_id = ?", ts[0].ID), 1)
	assertCount(t, db.Raw().Model(&chat.Conversation{}).Where("tenant_id = ?", ts[0].ID), 1)
}

func mustCreate(t *testing.T, db *gorm.DB, value any) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatal(err)
	}
}
func assertCount(t *testing.T, query *gorm.DB, want int64) {
	t.Helper()
	var got int64
	if err := query.Count(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count=%d want=%d", got, want)
	}
}

func TestProvisionConflictRollsBackBothTenants(t *testing.T) {
	db := tenantDB(t)
	ctx := context.Background()
	ts := tenants()
	mustCreate(t, db.Raw(), &auth.User{TenantID: "00000000-0000-4000-8000-000000000003", Email: ts[1].Employee.Email, FullName: "Other Tenant", Role: auth.RoleEmployee, PasswordHash: "existing", Active: true})
	uc := tenant.NewTenantUsecase(tenant.NewTenantRepository(db), nil)
	if err := uc.SetupTenants(ctx, ts, func(string) string { return "test-only-password" }); err == nil {
		t.Fatal("accepted cross-tenant email")
	}
	assertCount(t, db.Raw().Model(&auth.User{}), 1)
	assertCount(t, db.Raw().Model(&employee.Employee{}), 0)
	assertCount(t, db.Raw().Model(&chat.Conversation{}), 0)
}

func TestUnknownTenantResetDoesNotTouchStorage(t *testing.T) {
	db := tenantDB(t)
	uc := tenant.NewTenantUsecase(tenant.NewTenantRepository(db), func(context.Context, string) error { t.Fatal("unknown tenant reached storage"); return nil })
	if err := uc.Reset(context.Background(), tenants()[0].ID); err == nil {
		t.Fatal("accepted unknown tenant")
	}
}

func TestProvisionTenCandidateTenantsAgainstPostgres(t *testing.T) {
	db := tenantDB(t)
	uc := tenant.NewTenantUsecase(tenant.NewTenantRepository(db), nil)
	for range 2 {
		if err := uc.SetupTenants(context.Background(), tenCandidates(), func(string) string { return "test-only-password" }); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, db.Raw().Model(&auth.User{}), 20)
	for _, candidate := range tenCandidates() {
		assertCount(t, db.Raw().Model(&auth.User{}).Where("tenant_id = ? AND role = ? AND active = ?", candidate.ID, auth.RoleAdmin, true), 1)
		assertCount(t, db.Raw().Model(&auth.User{}).Where("tenant_id = ? AND role = ? AND active = ?", candidate.ID, auth.RoleEmployee, true), 1)
		assertCount(t, db.Raw().Model(&employee.Employee{}).Where("tenant_id = ?", candidate.ID), 1)
		assertCount(t, db.Raw().Model(&chat.Conversation{}).Where("tenant_id = ?", candidate.ID), 1)
	}
}
