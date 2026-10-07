package tenant_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aifgrouplaos/candidate-api/internal/tenant"
)

type repository struct {
	setup  func([]tenant.PreparedTenant) error
	revoke func(string) error
	clear  func(string) error
}

func (r repository) SetupTenants(_ context.Context, tenants []tenant.PreparedTenant) error {
	return r.setup(tenants)
}
func (r repository) Revoke(_ context.Context, id string) error { return r.revoke(id) }
func (r repository) Clear(_ context.Context, id string) error  { return r.clear(id) }

func TestResetCleanupFailureKeepsRecordsAndRevokesSessions(t *testing.T) {
	revoked, cleared := false, false
	failure := errors.New("storage offline")
	repo := repository{
		revoke: func(string) error { revoked = true; return nil },
		clear:  func(string) error { cleared = true; return nil },
	}
	uc := tenant.NewTenantUsecase(repo, func(context.Context, string) error {
		if !revoked {
			t.Fatal("cleanup ran before revocation")
		}
		return failure
	})
	err := uc.Reset(context.Background(), "00000000-0000-4000-8000-000000000001")
	if !errors.Is(err, failure) || !revoked || cleared {
		t.Fatalf("err=%v revoked=%v cleared=%v", err, revoked, cleared)
	}
}

func tenants() []tenant.Tenant {
	return []tenant.Tenant{
		{ID: "00000000-0000-4000-8000-000000000001", Admin: tenant.Account{Email: "admin1@example.test", FullName: "Admin One", PasswordEnv: "A1"}, Employee: tenant.Account{Email: "employee1@example.test", FullName: "Employee One", PasswordEnv: "E1"}},
		{ID: "00000000-0000-4000-8000-000000000002", Admin: tenant.Account{Email: "admin2@example.test", FullName: "Admin Two", PasswordEnv: "A2"}, Employee: tenant.Account{Email: "employee2@example.test", FullName: "Employee Two", PasswordEnv: "E2"}},
	}
}

func tenCandidates() []tenant.Tenant {
	result := make([]tenant.Tenant, 10)
	for i := range result {
		n := i + 1
		result[i] = tenant.Tenant{
			ID:       fmt.Sprintf("10000000-0000-4000-8000-%012d", n),
			Admin:    tenant.Account{Email: fmt.Sprintf("candidate%d.admin@example.test", n), FullName: "Candidate Admin", PasswordEnv: fmt.Sprintf("CANDIDATE_%d_ADMIN_PASSWORD", n)},
			Employee: tenant.Account{Email: fmt.Sprintf("candidate%d.employee@example.test", n), FullName: "Candidate Employee", PasswordEnv: fmt.Sprintf("CANDIDATE_%d_EMPLOYEE_PASSWORD", n)},
		}
	}
	return result
}

func TestSetupTenCandidates(t *testing.T) {
	var prepared []tenant.PreparedTenant
	manifest := tenCandidates()
	uc := tenant.NewTenantUsecase(repository{setup: func(ts []tenant.PreparedTenant) error { prepared = ts; return nil }}, nil)
	if err := uc.SetupTenants(context.Background(), manifest, func(string) string { return "test-only-password" }); err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 10 {
		t.Fatalf("tenants=%d; want 10", len(prepared))
	}
	for i, candidate := range prepared {
		if candidate.Tenant.ID != manifest[i].ID || candidate.EmployeeProfile.TenantID != candidate.Tenant.ID {
			t.Fatalf("candidate %d lost its tenant identity", i+1)
		}
	}
}

func TestSetupRejectsEmptyOrOversizedManifest(t *testing.T) {
	uc := tenant.NewTenantUsecase(repository{}, nil)
	for _, manifest := range [][]tenant.Tenant{nil, make([]tenant.Tenant, 101)} {
		if err := uc.SetupTenants(context.Background(), manifest, func(string) string { t.Fatal("invalid manifest read passwords"); return "" }); err == nil {
			t.Fatal("accepted an empty or oversized manifest")
		}
	}
}

func TestSetupValidatesAllAccountsBeforePersistence(t *testing.T) {
	called := false
	uc := tenant.NewTenantUsecase(repository{setup: func([]tenant.PreparedTenant) error { called = true; return nil }}, nil)
	for _, mutate := range []func([]tenant.Tenant){
		func(ts []tenant.Tenant) { ts[1].ID = ts[0].ID },
		func(ts []tenant.Tenant) { ts[1].Employee.Email = ts[0].Admin.Email },
		func(ts []tenant.Tenant) { ts[1].Employee.PasswordEnv = "MISSING" },
		func(ts []tenant.Tenant) { ts[0].Admin.FullName = "x" },
	} {
		ts := tenants()
		mutate(ts)
		err := uc.SetupTenants(context.Background(), ts, func(name string) string {
			if name == "MISSING" {
				return ""
			}
			return "test-only-password"
		})
		if err == nil || called {
			t.Fatalf("invalid manifest reached persistence: %v", err)
		}
	}
}

func TestResetRejectsInvalidTenantBeforeRevocation(t *testing.T) {
	uc := tenant.NewTenantUsecase(repository{}, func(context.Context, string) error { t.Fatal("cleanup should not run"); return nil })
	for _, id := range []string{"", "../other", "00000000-0000-0000-0000-000000000000", "{00000000-0000-4000-8000-000000000001}"} {
		if err := uc.Reset(context.Background(), id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	if err := tenant.NewTenantUsecase(repository{}, nil).Reset(context.Background(), tenants()[0].ID); err == nil {
		t.Fatal("accepted missing storage")
	}
}

func TestResetStopsOnRevocationFailureAndCanBeRepeated(t *testing.T) {
	failure := errors.New("unknown tenant")
	uc := tenant.NewTenantUsecase(repository{revoke: func(string) error { return failure }}, func(context.Context, string) error { t.Fatal("cleanup ran after failed revocation"); return nil })
	if err := uc.Reset(context.Background(), tenants()[0].ID); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	cleared := 0
	uc = tenant.NewTenantUsecase(repository{revoke: func(string) error { return nil }, clear: func(string) error { cleared++; return nil }}, func(context.Context, string) error { return nil })
	for range 2 {
		if err := uc.Reset(context.Background(), tenants()[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if cleared != 2 {
		t.Fatalf("clear calls=%d", cleared)
	}
}
