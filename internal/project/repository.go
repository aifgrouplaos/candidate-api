package project

import (
	"context"
	"errors"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
	"github.com/aifgrouplaos/candidate-api/pkg/utils"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const (
	uniqueViolation  = "23505"
	tenantCodeIndex  = "idx_projects_tenant_code"
	bySortOrder      = "sort_order"
	byPositionClause = "position"
	whereTenantKey   = "tenant_id = ? AND idempotency_key = ?"
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
	project, err := r.find(ctx, "id = ? AND tenant_id = ?", id, tenantID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	return project, err
}

func (r *projectRepository) List(ctx context.Context, filter ListFilter) ([]*Project, int64, error) {
	query := r.db.Session(ctx).Model(&Project{}).Where("tenant_id = ?", filter.TenantID)
	if filter.Search != "" {
		pattern := utils.ContainsPattern(filter.Search)
		query = query.Where("(name ILIKE ? OR code ILIKE ?)", pattern, pattern)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	projects := []*Project{}
	err := query.Select(`projects.*,
		(SELECT COUNT(*) FROM project_phases ph WHERE ph.project_id = projects.id) AS total_phases,
		(SELECT COUNT(*) FROM project_tasks t JOIN project_phases ph ON ph.id = t.phase_id WHERE ph.project_id = projects.id) AS total_tasks`).
		Order("created_at DESC").Order("id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&projects).Error
	return projects, total, err
}

func (r *projectRepository) Delete(ctx context.Context, tenantID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return errNotFound
	}
	result := r.db.Session(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&Project{})
	if result.Error == nil && result.RowsAffected == 0 {
		return errNotFound
	}
	return result.Error
}

func (r *projectRepository) FindByIdempotencyKey(ctx context.Context, tenantID, key string) (*Project, error) {
	project, err := r.find(ctx, whereTenantKey, tenantID, key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return project, err
}

func (r *projectRepository) find(ctx context.Context, where string, args ...any) (*Project, error) {
	byOrder := func(db *gorm.DB) *gorm.DB { return db.Order(bySortOrder) }
	byPosition := func(db *gorm.DB) *gorm.DB { return db.Order(byPositionClause) }
	var project Project
	err := r.db.Session(ctx).Preload("Phases", byOrder).Preload("Phases.Tasks", byPosition).
		Where(where, args...).First(&project).Error
	return &project, err
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

func (r *projectRepository) Create(ctx context.Context, project *Project) error {
	err := r.db.Transaction(ctx, func(tx *gorm.DB) error {
		// Serializes submissions that share a key, so a retry racing the original waits and
		// then replays it instead of failing on the Project code.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "projects:"+project.TenantID+":"+project.IdempotencyKey).Error; err != nil {
			return err
		}
		var used int64
		if err := tx.Model(&Project{}).Where(whereTenantKey, project.TenantID, project.IdempotencyKey).Count(&used).Error; err != nil {
			return err
		}
		if used != 0 {
			return errKeyUsed
		}
		return tx.Create(project).Error
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == tenantCodeIndex {
		return errs.Conflict("The project code is already in use.")
	}
	return err
}
