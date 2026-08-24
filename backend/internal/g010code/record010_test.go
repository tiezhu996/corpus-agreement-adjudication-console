package g010code

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
	engine    *gin.Engine
	db        *gorm.DB
	manager   string
	annotatorA string
	datasetID uint
	schemaID  uint
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
	var dataset model.CorpusDataset
	if err := db.Where("dataset_code = ?", "NLP-LEGAL-ZH").First(&dataset).Error; err != nil {
		t.Fatalf("seeded dataset missing: %v", err)
	}
	var schema model.AnnotationSchema
	if err := db.Where("schema_code = ? AND schema_state = ?", "LEGAL-ENTITY", "published").First(&schema).Error; err != nil {
		t.Fatalf("seeded schema missing: %v", err)
	}
	engine := router.New(db, cfg)
	return &env{
		engine: engine, db: db,
		manager: login(t, engine, "manager", "Data#536"),
		annotatorA: login(t, engine, "annotator_a", "Annotate#536"),
		datasetID: dataset.ID, schemaID: schema.ID,
	}
}

func login(t *testing.T, engine *gin.Engine, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed for %s: %d %s", username, rec.Code, rec.Body.String())
	}
	var loginResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil || loginResp.Data.Token == "" {
		t.Fatalf("login response malformed: %v", err)
	}
	return loginResp.Data.Token
}

func TestG010DatasetCreateReturns201(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_code": "CODE-DS-1", "name": "Status corpus", "language": "en",
		"domain": "quality", "document_count": 1,
		"content_mask_policy": "Mask all identifiers before review.", "owner_team": "Quality",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.manager)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating a dataset, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG010DatasetTransitionReturns200(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_code": "CODE-TRN-1", "name": "Status corpus", "language": "en",
		"domain": "quality", "document_count": 1,
		"content_mask_policy": "Mask all identifiers before review.", "owner_team": "Quality",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.manager)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("create dataset malformed: %s", rec.Body.String())
	}
	raw2, _ := json.Marshal(map[string]any{"target_state": "frozen", "version": 1})
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/datasets/%d/transition", resp.Data.ID), bytes.NewReader(raw2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+e.manager)
	rec2 := httptest.NewRecorder()
	e.engine.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 when transitioning a dataset, got %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestG010SchemaCreateReturns201(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": e.datasetID, "schema_code": "CODE-SCH", "version": 1,
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
		t.Fatalf("expected 201 when creating a schema, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG010SchemaCopyReturns201(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": e.datasetID, "schema_code": "CODE-CPY", "version": 1,
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
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("create schema malformed: %s", rec.Body.String())
	}
	raw2, _ := json.Marshal(map[string]any{"version": 9})
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/schemas/%d/copy", resp.Data.ID), bytes.NewReader(raw2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+e.manager)
	rec2 := httptest.NewRecorder()
	e.engine.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected 201 when copying a schema, got %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestG010SchemaTransitionReturns200(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": e.datasetID, "schema_code": "CODE-VAL", "version": 1,
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
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("create schema malformed: %s", rec.Body.String())
	}
	raw2, _ := json.Marshal(map[string]any{"target_state": "validated"})
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/schemas/%d/transition", resp.Data.ID), bytes.NewReader(raw2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+e.manager)
	rec2 := httptest.NewRecorder()
	e.engine.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 when transitioning a schema, got %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestG010AnnotationCreateReturns201(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": e.datasetID, "schema_id": e.schemaID, "item_key": "CODE-ANN-1",
		"labels": []map[string]any{{"unit_key": "document_class", "label": "RISK"}},
		"source_checksum": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"quality_note": "status code test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.annotatorA)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating an annotation, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG010AnnotationTransitionReturns200(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": e.datasetID, "schema_id": e.schemaID, "item_key": "CODE-ANN-2",
		"labels": []map[string]any{{"unit_key": "document_class", "label": "RISK"}},
		"source_checksum": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"quality_note": "status code test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.annotatorA)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("create annotation malformed: %s", rec.Body.String())
	}
	raw2, _ := json.Marshal(map[string]any{"target_state": "submitted", "reason": "submitting"})
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/annotations/%d/transition", resp.Data.ID), bytes.NewReader(raw2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+e.annotatorA)
	rec2 := httptest.NewRecorder()
	e.engine.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 when transitioning an annotation, got %d %s", rec2.Code, rec2.Body.String())
	}
}
