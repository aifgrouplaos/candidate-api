package project

import (
	"context"
	"errors"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const (
	uniqueViolation  = "23505"
	tenantCodeIndex  = "idx_projects_tenant_code"
	byPositionClause = "position"
)

var errNotFound = *errs.NotFound("Project not found.")

type projectRepository struct {
	db contract.ORM
}

func NewProjectRepository(db contract.ORM) ProjectRepository {
	return &projectRepository{db: db}
}

func (r *projectRepository) FindByID(ctx context.Context, tenantID, id string) (*Project, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errNotFound
	}
	byPosition := func(db *gorm.DB) *gorm.DB { return db.Order(byPositionClause) }
	var project Project
	err := r.db.Session(ctx).Preload("Phases", byPosition).Preload("Phases.Tasks", byPosition).
		Where("id = ? AND tenant_id = ?", id, tenantID).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	return &project, err
}

func (r *projectRepository) FindIdempotencyKey(ctx context.Context, tenantID, key string) (*IdempotencyKey, error) {
	var found IdempotencyKey
	err := r.db.Session(ctx).Where("tenant_id = ? AND key = ?", tenantID, key).First(&found).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &found, err
}

func (r *projectRepository) ActiveEmployeeIDs(ctx context.Context, tenantID string, ids []string) (map[string]bool, error) {
	var found []string
	err := r.db.Session(ctx).Model(&employee.Employee{}).
		Where("tenant_id = ? AND status = ? AND id IN ?", tenantID, employee.StatusActive, ids).
		Pluck("id", &found).Error
	active := make(map[string]bool, len(found))
	for _, id := range found {
		active[id] = true
	}
	return active, err
}

func (r *projectRepository) Create(ctx context.Context, project *Project, key *IdempotencyKey) error {
	err := r.db.Transaction(ctx, func(tx *gorm.DB) error {
		// Serializes submissions that share a key, so a retry racing the original waits and
		// then replays it instead of failing on the Project code.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "projects:"+key.TenantID+":"+key.Key).Error; err != nil {
			return err
		}
		var used int64
		if err := tx.Model(&IdempotencyKey{}).Where("tenant_id = ? AND key = ?", key.TenantID, key.Key).Count(&used).Error; err != nil {
			return err
		}
		if used != 0 {
			return errKeyUsed
		}
		if err := tx.Create(project).Error; err != nil {
			return err
		}
		key.ProjectID = project.ID
		return tx.Create(key).Error
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == tenantCodeIndex {
		return errs.Conflict("The project code is already in use.")
	}
	return err
}
