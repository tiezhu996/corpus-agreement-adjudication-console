package g004perm

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
	"corpus-annotation-agreement-control/backend/internal/dto"
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

func createAnnotationRaw(t *testing.T, e *env, token, itemKey, checksum string, labels []map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": e.datasetID, "schema_id": e.schemaID, "item_key": itemKey,
		"labels": labels, "source_checksum": checksum, "quality_note": "norm test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	return rec
}

func TestG004LowercaseLabelNormalized(t *testing.T) {
	e := newTestEnv(t)
	rec := createAnnotationRaw(t, e, e.annotatorA, "NORM-LOW-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa41",
		[]map[string]any{{"unit_key": "document_class", "label": "risk"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 with lowercase label normalized, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Labels []struct {
				Label string `json:"label"`
			} `json:"labels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Data.Labels) == 0 {
		t.Fatalf("annotation response malformed: %s", rec.Body.String())
	}
	if resp.Data.Labels[0].Label != "RISK" {
		t.Fatalf("expected the label normalized to uppercase RISK, got %q", resp.Data.Labels[0].Label)
	}
}

func TestG004DuplicateUnitRejected(t *testing.T) {
	e := newTestEnv(t)
	rec := createAnnotationRaw(t, e, e.annotatorA, "NORM-DUP-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa42",
		[]map[string]any{
			{"unit_key": "document_class", "label": "RISK"},
			{"unit_key": "document_class", "label": "CLEAR"},
		})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for duplicate classification units, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG004InvalidSpanRejected(t *testing.T) {
	e := newTestEnv(t)
	rec := createAnnotationRaw(t, e, e.annotatorA, "NORM-SPN-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa43",
		[]map[string]any{{"unit_key": "masked_clause", "label": "PARTY", "start": 5, "end": 5}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an invalid span, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG004ClassificationNotSpan(t *testing.T) {
	label := dto.AnnotationLabel{UnitKey: "document_class", Label: "RISK"}
	if label.IsSpan() {
		t.Fatal("expected a classification label (zero offsets) not to be treated as a span")
	}
}

func TestG004UnitKeyTrimmed(t *testing.T) {
	e := newTestEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"dataset_id": e.datasetID, "schema_id": e.schemaID, "item_key": "NORM-KEY-1",
		"labels": []map[string]any{{"unit_key": "  document_class  ", "label": "RISK"}},
		"source_checksum": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa45",
		"quality_note": "unit key trim test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.annotatorA)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating annotation, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Labels []struct {
				UnitKey string `json:"unit_key"`
			} `json:"labels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Data.Labels) == 0 {
		t.Fatalf("annotation response malformed: %s", rec.Body.String())
	}
	if resp.Data.Labels[0].UnitKey != "document_class" {
		t.Fatalf("expected the unit key trimmed, got %q", resp.Data.Labels[0].UnitKey)
	}
}
