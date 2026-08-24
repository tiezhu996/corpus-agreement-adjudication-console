package g001err404

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"corpus-annotation-agreement-control/backend/internal/config"
	"corpus-annotation-agreement-control/backend/internal/router"
)

func loginManager(t *testing.T, engine *gin.Engine) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "manager", "password": "Data#536"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	var login struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil || login.Data.Token == "" {
		t.Fatalf("login response malformed: %v", err)
	}
	return login.Data.Token
}

func newTestRouter(t *testing.T) (*gin.Engine, string) {
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
	return engine, loginManager(t, engine)
}

func TestG001DatasetDetailMissingReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/datasets/999999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing dataset detail, got %d", rec.Code)
	}
}

func TestG001DatasetUpdateMissingReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	payload, _ := json.Marshal(map[string]any{
		"name": "Renamed", "language": "en", "domain": "quality",
		"document_count": 1, "content_mask_policy": "Mask all identifiers before review.",
		"owner_team": "Quality", "version": 1,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/datasets/999999", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for updating missing dataset, got %d", rec.Code)
	}
}

func TestG001DatasetTransitionMissingReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	payload, _ := json.Marshal(map[string]any{"target_state": "frozen", "version": 1})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/999999/transition", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for transitioning missing dataset, got %d", rec.Code)
	}
}

func TestG001SchemaDetailMissingReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/999999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing schema detail, got %d", rec.Code)
	}
}

func TestG001SchemaUpdateMissingReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	payload, _ := json.Marshal(map[string]any{
		"label_definitions": []map[string]any{
			{"code": "RISK", "display_name": "Risk", "task_type": "classification", "description": "Material contractual risk."},
			{"code": "CLEAR", "display_name": "Clear", "task_type": "classification", "description": "No material risk."},
		},
		"span_policy": "Half-open offsets; exclude punctuation.",
		"overlap_policy": "Nested spans forbidden.",
		"examples": []map[string]any{{"item_key": "EX-1", "masked_text": "[MASK] provides ***.", "labels": json.RawMessage(`[{"unit_key":"c","label":"RISK"}]`)}},
		"version": 1,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/schemas/999999", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for updating missing schema, got %d", rec.Code)
	}
}

func TestG001SchemaCopyMissingReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	payload, _ := json.Marshal(map[string]any{"version": 5})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/999999/copy", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for copying missing schema, got %d", rec.Code)
	}
}

func TestG001SchemaTransitionMissingReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	payload, _ := json.Marshal(map[string]any{"target_state": "validated"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/999999/transition", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for transitioning missing schema, got %d", rec.Code)
	}
}

func TestG001SchemaCreateMissingDatasetReturns404(t *testing.T) {
	engine, token := newTestRouter(t)
	payload, _ := json.Marshal(map[string]any{
		"dataset_id": 999999, "schema_code": "MISSING-DS", "version": 1,
		"label_definitions": []map[string]any{
			{"code": "RISK", "display_name": "Risk", "task_type": "classification", "description": "Material contractual risk."},
			{"code": "CLEAR", "display_name": "Clear", "task_type": "classification", "description": "No material risk."},
		},
		"span_policy": "Half-open offsets; exclude punctuation.",
		"overlap_policy": "Nested spans forbidden.",
		"examples": []map[string]any{{"item_key": "EX-1", "masked_text": "[MASK] provides ***.", "labels": json.RawMessage(`[{"unit_key":"c","label":"RISK"}]`)}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for creating schema on missing dataset, got %d", rec.Code)
	}
}
