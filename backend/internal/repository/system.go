package repository

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"corpus-annotation-agreement-control/backend/internal/model"
)

var (
	ErrVersionConflict = errors.New("record version changed")
	ErrStateConflict   = errors.New("record state changed")
)

// IsUniqueViolation reports whether err is a unique-constraint violation coming
// from either the PostgreSQL or the SQLite driver. gorm.io/driver/postgres wraps
// the underlying network error as *pgconn.PgError (SQLSTATE class 23), while the
// SQLite driver surfaces a localized "UNIQUE constraint failed" message.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return strings.HasPrefix(pgError.Code, "23")
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

type SystemRepository struct{ db *gorm.DB }

func NewSystemRepository(db *gorm.DB) *SystemRepository { return &SystemRepository{db: db} }
func (repository *SystemRepository) WithDB(db *gorm.DB) *SystemRepository {
	return &SystemRepository{db: db}
}
func (repository *SystemRepository) DB() *gorm.DB { return repository.db }

func (repository *SystemRepository) FindUser(username string) (model.User, error) {
	var user model.User
	if err := repository.db.Where("username = ? AND active = ?", username, true).First(&user).Error; err != nil {
		return user, fmt.Errorf("find active user: %w", err)
	}
	return user, nil
}

func (repository *SystemRepository) CreateAudit(event *model.AuditEvent) error {
	if err := repository.db.Create(event).Error; err != nil {
		return fmt.Errorf("create audit event: %w", err)
	}
	return nil
}

func (repository *SystemRepository) ListAudit(page, pageSize int, actor, requestID, resourceType, action string, from, to *time.Time) ([]model.AuditEvent, int64, error) {
	query := repository.db.Model(&model.AuditEvent{})
	if actor != "" {
		query = query.Where("actor = ?", actor)
	}
	if requestID != "" {
		query = query.Where("request_id = ?", requestID)
	}
	if resourceType != "" {
		query = query.Where("resource_type = ?", resourceType)
	}
	if action != "" {
		query = query.Where("action = ?", action)
	}
	if from != nil {
		query = query.Where("created_at >= ?", *from)
	}
	if to != nil {
		query = query.Where("created_at < ?", *to)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit events: %w", err)
	}
	var events []model.AuditEvent
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&events).Error; err != nil {
		return nil, 0, fmt.Errorf("list audit events: %w", err)
	}
	return events, total, nil
}
