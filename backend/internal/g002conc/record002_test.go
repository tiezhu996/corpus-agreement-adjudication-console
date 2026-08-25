package g002conc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"corpus-annotation-agreement-control/backend/internal/config"
	"corpus-annotation-agreement-control/backend/internal/model"
	"corpus-annotation-agreement-control/backend/internal/repository"
	"corpus-annotation-agreement-control/backend/internal/router"
)

func newTestRouter(t *testing.T) (*gin.Engine, string, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		Port: "0", DBDriver: "sqlite",
		DBDSN:              fmt.Sprintf("file:%s/test.db?_pragma=busy_timeout(60000)&_pragma=journal_mode(WAL)", t.TempDir()),
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
	return engine, login(t, engine, "manager", "Data#536"), db
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

func createDataset(t *testing.T, engine *gin.Engine, token, code string) uint {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"dataset_code": code, "name": "Concurrency corpus", "language": "en",
		"domain": "quality", "document_count": 2,
		"content_mask_policy": "Mask all identifiers before review.", "owner_team": "Quality",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dataset failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("create dataset response malformed: %v", err)
	}
	return resp.Data.ID
}

func createSchema(t *testing.T, engine *gin.Engine, token string, datasetID uint) uint {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"dataset_id": datasetID, "schema_code": "CONC-SCH", "version": 1,
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
	if rec.Code != http.StatusCreated {
		t.Fatalf("create schema failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("create schema response malformed: %v", err)
	}
	return resp.Data.ID
}

func createAnnotation(t *testing.T, engine *gin.Engine, token string, datasetID, schemaID uint) uint {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"dataset_id": datasetID, "schema_id": schemaID, "item_key": "CONC-ITEM-1",
		"labels": []map[string]any{{"unit_key": "document_class", "label": "RISK"}},
		"source_checksum": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"quality_note": "concurrency test annotation",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create annotation failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("create annotation response malformed: %v", err)
	}
	return resp.Data.ID
}


func freezeAndPublish(t *testing.T, engine *gin.Engine, token string, datasetID, schemaID uint) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"target_state": "frozen", "version": 1})
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/datasets/%d/transition", datasetID), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("freeze dataset failed: %d %s", rec.Code, rec.Body.String())
	}
	payload, _ = json.Marshal(map[string]any{"target_state": "validated"})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/schemas/%d/transition", schemaID), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("validate schema failed: %d %s", rec.Code, rec.Body.String())
	}
	payload, _ = json.Marshal(map[string]any{"target_state": "published"})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/schemas/%d/transition", schemaID), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish schema failed: %d %s", rec.Code, rec.Body.String())
	}
}

func raceCodes(t *testing.T, n int, fn func()) []int {
	t.Helper()
	start := make(chan struct{})
	statuses := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			<-start
			fn()
		}()
	}
	close(start)
	codes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		codes = append(codes, <-statuses)
	}
	return codes
}

func TestG002ConcurrentDatasetUpdateOneConflicts(t *testing.T) {
	engine, token, db := newTestRouter(t)
	id := createDataset(t, engine, token, "CONC-UPD-1")
	repo := repository.NewCorpusDatasetRepository(db)
	var dataset model.CorpusDataset
	if err := db.First(&dataset, id).Error; err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	dataset.Name = "Renamed concurrent"
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- repo.Update(&dataset, 1)
		}()
	}
	close(start)
	errs := []error{<-results, <-results}
	conflicts := 0
	for _, e := range errs {
		if errors.Is(e, repository.ErrVersionConflict) {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly one version conflict for concurrent dataset update, got %v", errs)
	}
}

func TestG002ConcurrentDatasetTransitionOneConflicts(t *testing.T) {
	engine, token, db := newTestRouter(t)
	id := createDataset(t, engine, token, "CONC-TRN-1")
	datasetRepo := repository.NewCorpusDatasetRepository(db)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- datasetRepo.Transition(id, 1, "draft", "frozen")
		}()
	}
	close(start)
	errs := []error{<-results, <-results}
	conflicts := 0
	for _, e := range errs {
		if errors.Is(e, repository.ErrStateConflict) {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly one state conflict for concurrent dataset transition, got %v", errs)
	}
}

func TestG002ConcurrentSchemaEditOneConflicts(t *testing.T) {
	engine, token, db := newTestRouter(t)
	datasetID := createDataset(t, engine, token, "CONC-SCH-1")
	schemaID := createSchema(t, engine, token, datasetID)
	repo := repository.NewAnnotationSchemaRepository(db)
	var schema model.AnnotationSchema
	if err := db.First(&schema, schemaID).Error; err != nil {
		t.Fatalf("load schema: %v", err)
	}
	schema.SpanPolicy = "Updated span policy; half-open offsets."
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- repo.UpdateDraft(&schema, schema.UpdatedAt)
		}()
	}
	close(start)
	errs := []error{<-results, <-results}
	conflicts := 0
	for _, e := range errs {
		if errors.Is(e, repository.ErrVersionConflict) {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly one version conflict for concurrent schema edit, got %v", errs)
	}
}

func TestG002ConcurrentSchemaTransitionOneConflicts(t *testing.T) {
	engine, token, db := newTestRouter(t)
	datasetID := createDataset(t, engine, token, "CONC-SCH-2")
	schemaID := createSchema(t, engine, token, datasetID)
	schemaRepo := repository.NewAnnotationSchemaRepository(db)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- schemaRepo.Transition(schemaID, "draft", "validated")
		}()
	}
	close(start)
	errs := []error{<-results, <-results}
	conflicts := 0
	for _, e := range errs {
		if errors.Is(e, repository.ErrStateConflict) {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly one state conflict for concurrent schema transition, got %v", errs)
	}
}

func TestG002ConcurrentAnnotationEditOneConflicts(t *testing.T) {
	engine, token, db := newTestRouter(t)
	annotatorToken := login(t, engine, "annotator_a", "Annotate#536")
	datasetID := createDataset(t, engine, token, "CONC-ANN-1")
	schemaID := createSchema(t, engine, token, datasetID)
	freezeAndPublish(t, engine, token, datasetID, schemaID)
	annID := createAnnotation(t, engine, annotatorToken, datasetID, schemaID)
	repo := repository.NewAnnotationSetRepository(db)
	var annotation model.AnnotationSet
	if err := db.First(&annotation, annID).Error; err != nil {
		t.Fatalf("load annotation: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- repo.UpdateDraft(annotation.ID, annotation.AnnotatorID, annotation.UpdatedAt, `[{"unit_key":"document_class","label":"CLEAR"}]`, "concurrent edit attempt")
		}()
	}
	close(start)
	errs := []error{<-results, <-results}
	conflicts := 0
	for _, e := range errs {
		if errors.Is(e, repository.ErrStateConflict) {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly one state conflict for concurrent annotation edit, got %v", errs)
	}
}

func TestG002ConcurrentAnnotationTransitionOneConflicts(t *testing.T) {
	engine, token, db := newTestRouter(t)
	annotatorToken := login(t, engine, "annotator_a", "Annotate#536")
	datasetID := createDataset(t, engine, token, "CONC-ANN-2")
	schemaID := createSchema(t, engine, token, datasetID)
	freezeAndPublish(t, engine, token, datasetID, schemaID)
	annID := createAnnotation(t, engine, annotatorToken, datasetID, schemaID)
	annotationRepo := repository.NewAnnotationSetRepository(db)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- annotationRepo.Transition(annID, "draft", "submitted")
		}()
	}
	close(start)
	errs := []error{<-results, <-results}
	conflicts := 0
	for _, e := range errs {
		if errors.Is(e, repository.ErrStateConflict) {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly one state conflict for concurrent annotation transition, got %v", errs)
	}
}
