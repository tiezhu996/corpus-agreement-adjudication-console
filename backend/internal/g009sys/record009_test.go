package g009sys

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"corpus-annotation-agreement-control/backend/internal/config"
	"corpus-annotation-agreement-control/backend/internal/model"
	"corpus-annotation-agreement-control/backend/internal/router"
)

type env struct {
	engine  *gin.Engine
	db      *gorm.DB
	manager string
	auditor string
}

func newTestEnv(t *testing.T) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		Port: "0", DBDriver: "sqlite",
		DBDSN:              fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()),
		DBAutoMigrate:      true,
		JWTSecret:          "test-secret-with-at-least-24-bytes-xyz",
		JWTTTL:             time.Hour,
		CORSOrigin:         "http://localhost:18536",
		LogLevel:           "info",
		RateLimitPerMinute: 100000,
		AlgorithmVersion:   "agreement-nominal-span-v1.0",
	}
	db, err := config.OpenDatabase(cfg)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(db, cfg)
	return &env{engine: engine, db: db, manager: tokenOf(t, login(t, engine, "manager", "Data#536")), auditor: tokenOf(t, login(t, engine, "auditor", "Audit#536"))}
}

func login(t *testing.T, engine *gin.Engine, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func tokenOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.Token == "" {
		t.Fatalf("login response malformed: %s", rec.Body.String())
	}
	return resp.Data.Token
}

func TestG009DeactivatedUserCannotLogin(t *testing.T) {
	e := newTestEnv(t)
	if err := e.db.Model(&model.User{}).Where("username = ?", "annotator_a").Update("active", false).Error; err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	rec := login(t, e.engine, "annotator_a", "Annotate#536")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a deactivated user, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG009DuplicateCodeReturnsConflict(t *testing.T) {
	e := newTestEnv(t)
	payload := map[string]any{
		"dataset_code": "DUP-CODE-1", "name": "Duplicate corpus", "language": "en",
		"domain": "quality", "document_count": 1,
		"content_mask_policy": "Mask all identifiers before review.", "owner_team": "Quality",
	}
	raw, _ := json.Marshal(payload)
	first := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(raw))
	first.Header.Set("Content-Type", "application/json")
	first.Header.Set("Authorization", "Bearer "+e.manager)
	rec1 := httptest.NewRecorder()
	e.engine.ServeHTTP(rec1, first)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first create failed: %d %s", rec1.Code, rec1.Body.String())
	}
	second := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(raw))
	second.Header.Set("Content-Type", "application/json")
	second.Header.Set("Authorization", "Bearer "+e.manager)
	rec2 := httptest.NewRecorder()
	e.engine.ServeHTTP(rec2, second)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a duplicate dataset code, got %d %s", rec2.Code, rec2.Body.String())
	}
}

func auditList(t *testing.T, e *env, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit"+query, nil)
	req.Header.Set("Authorization", "Bearer "+e.auditor)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	return rec
}

func createDatasetRaw(t *testing.T, e *env, code string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"dataset_code": code, "name": "Audit corpus", "language": "en",
		"domain": "quality", "document_count": 1,
		"content_mask_policy": "Mask all identifiers before review.", "owner_team": "Quality",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.manager)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dataset failed: %d %s", rec.Code, rec.Body.String())
	}
}

func createSchemaRaw(t *testing.T, e *env, datasetID uint) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": datasetID, "schema_code": "AUDIT-SCH", "version": 1,
		"label_definitions": []map[string]any{
			{"code": "RISK", "display_name": "Risk", "task_type": "classification", "description": "Material contractual risk."},
			{"code": "CLEAR", "display_name": "Clear", "task_type": "classification", "description": "No material risk."},
		},
		"span_policy": "Half-open offsets; exclude punctuation.",
		"overlap_policy": "Nested spans forbidden.",
		"examples": []map[string]any{{"item_key": "EX-1", "masked_text": "[MASK] provides ***.", "labels": json.RawMessage(`[{"unit_key":"c","label":"RISK"}]`)}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.manager)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create schema failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestG009AuditActionFilterWorks(t *testing.T) {
	e := newTestEnv(t)
	createDatasetRaw(t, e, "AUDIT-DS-1")
	createDatasetRaw(t, e, "AUDIT-DS-2")
	var dataset model.CorpusDataset
	if err := e.db.Where("dataset_code = ?", "AUDIT-DS-1").First(&dataset).Error; err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	createSchemaRaw(t, e, dataset.ID)
	rec := auditList(t, e, "?action=corpus_dataset.created")
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			Action string `json:"action"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("audit response malformed: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("expected audit events for dataset creation")
	}
	for _, item := range resp.Data {
		if item.Action != "corpus_dataset.created" {
			t.Fatalf("action filter leaked event %q", item.Action)
		}
	}
}

func TestG009AuditResourceFilterWorks(t *testing.T) {
	e := newTestEnv(t)
	createDatasetRaw(t, e, "AUDIT-DS-3")
	var dataset model.CorpusDataset
	if err := e.db.Where("dataset_code = ?", "AUDIT-DS-3").First(&dataset).Error; err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	createSchemaRaw(t, e, dataset.ID)
	rec := auditList(t, e, "?resource_type=corpus_dataset")
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			ResourceType string `json:"resource_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("audit response malformed: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("expected audit events for dataset resource")
	}
	for _, item := range resp.Data {
		if item.ResourceType != "corpus_dataset" {
			t.Fatalf("resource filter leaked event %q", item.ResourceType)
		}
	}
}

func TestG009InvalidTimeFilterRejected(t *testing.T) {
	e := newTestEnv(t)
	rec := auditList(t, e, "?from=not-a-time")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid from filter, got %d %s", rec.Code, rec.Body.String())
	}
}
