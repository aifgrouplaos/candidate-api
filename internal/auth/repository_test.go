package auth

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/config"
)

func TestCreateSessionPrunesDeadRowsAfterRetention(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	admin, err := gormadapter.New(config.Postgres{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("auth_test_%d", time.Now().UnixNano())
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
	if err := db.Raw().AutoMigrate(&AuthSession{}, &RefreshToken{}); err != nil {
		t.Fatal(err)
	}

	const user, other, tenant = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	now := time.Now().UTC()
	old, recent, live := now.Add(-8*24*time.Hour), now.Add(-time.Hour), now.Add(time.Hour)
	sessions := []struct {
		id, user  string
		revokedAt *time.Time
		tokens    []RefreshToken
		kept      bool
	}{
		{id: "00000000-0000-4000-8000-000000000001", user: user, revokedAt: &old, tokens: []RefreshToken{{RevokedAt: &old, ExpiresAt: live}}},
		{id: "00000000-0000-4000-8000-000000000002", user: user, tokens: []RefreshToken{{ExpiresAt: old}}},
		{id: "00000000-0000-4000-8000-000000000003", user: user, revokedAt: &recent, tokens: []RefreshToken{{RevokedAt: &recent, ExpiresAt: live}}, kept: true},
		{id: "00000000-0000-4000-8000-000000000004", user: user, tokens: []RefreshToken{{RevokedAt: &old, ExpiresAt: live}, {ExpiresAt: live}}, kept: true},
		{id: "00000000-0000-4000-8000-000000000005", user: other, revokedAt: &old, tokens: []RefreshToken{{RevokedAt: &old, ExpiresAt: live}}, kept: true},
	}
	for i, s := range sessions {
		if err := db.Raw().Create(&AuthSession{ID: s.id, UserID: s.user, TenantID: tenant, RevokedAt: s.revokedAt}).Error; err != nil {
			t.Fatal(err)
		}
		for j, token := range s.tokens {
			token.UserID, token.SessionID, token.TokenHash = s.user, s.id, fmt.Sprintf("hash-%d-%d", i, j)
			if err := db.Raw().Create(&token).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	login := &AuthSession{ID: "00000000-0000-4000-8000-000000000009", UserID: user, TenantID: tenant}
	if err := NewAuthRepository(db).CreateSession(context.Background(), login, &RefreshToken{TokenHash: "new", ExpiresAt: live}); err != nil {
		t.Fatal(err)
	}

	for _, s := range sessions {
		var count int64
		db.Raw().Model(&AuthSession{}).Where("id = ?", s.id).Count(&count)
		if (count == 1) != s.kept {
			t.Errorf("session %s kept = %v, want %v", s.id, count == 1, s.kept)
		}
	}
	var tokens int64
	db.Raw().Model(&RefreshToken{}).Where("user_id = ?", user).Count(&tokens)
	if tokens != 3 {
		t.Errorf("user refresh tokens = %d, want 3 (one recent revoked, one live, one new)", tokens)
	}
	var active int64
	db.Raw().Model(&AuthSession{}).Where("user_id = ? AND revoked_at IS NULL", user).Count(&active)
	if active != 1 {
		t.Errorf("active sessions = %d, want only the new login", active)
	}
}
