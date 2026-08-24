package g005idem

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
	"corpus-annotation-agreement-control/backend/internal/repository"
	"corpus-annotation-agreement-control/backend/internal/router"
	"corpus-annotation-agreement-control/backend/internal/service"
)

type env struct {
	engine    *gin.Engine
	db        *gorm.DB
	manager   string
	adjudicator string
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
		adjudicator: login(t, engine, "adjudicator", "Decide#536"),
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

func request(t *testing.T, e *env, token, method, path string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	return rec
}

func createAnnotation(t *testing.T, e *env, token, itemKey, checksum, label string) uint {
	t.Helper()
	rec := request(t, e, token, http.MethodPost, "/api/v1/annotations", map[string]any{
		"dataset_id": e.datasetID, "schema_id": e.schemaID, "item_key": itemKey,
		"labels": []map[string]any{{"unit_key": "document_class", "label": label}},
		"source_checksum": checksum, "quality_note": "idempotency test",
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create annotation failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == 0 {
		t.Fatalf("annotation response malformed: %s", rec.Body.String())
	}
	return resp.Data.ID
}

func comparedPair(t *testing.T, e *env, itemKey string, checksumA, checksumB string) (uint, uint) {
	t.Helper()
	tokenA := login(t, e.engine, "annotator_a", "Annotate#536")
	tokenB := login(t, e.engine, "annotator_b", "Compare#536")
	a := createAnnotation(t, e, tokenA, itemKey, checksumA, "RISK")
	b := createAnnotation(t, e, tokenB, itemKey, checksumB, "CLEAR")
	for _, id := range []uint{a, b} {
		for _, target := range []string{"submitted", "locked", "compared"} {
			tok := tokenA
			if id == b {
				tok = tokenB
			}
			who := e.manager
			if target == "submitted" {
				who = tok
			}
			rec := request(t, e, who, http.MethodPost, fmt.Sprintf("/api/v1/annotations/%d/transition", id), map[string]any{"target_state": target, "reason": "idem chain"}, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("transition %s of %d failed: %d %s", target, id, rec.Code, rec.Body.String())
			}
		}
	}
	return a, b
}

func compute(t *testing.T, e *env, key, itemKey string, ids []uint) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, e, e.manager, http.MethodPost, "/api/v1/adjudications", map[string]any{
		"dataset_id": e.datasetID, "item_key": itemKey, "annotation_set_ids": ids, "metric": "auto",
	}, map[string]string{"Idempotency-Key": key})
}

func TestG005SameKeyDifferentInputConflicts(t *testing.T) {
	e := newTestEnv(t)
	a1, b1 := comparedPair(t, e, "G005-ITEM-A", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa11", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa12")
	a2, b2 := comparedPair(t, e, "G005-ITEM-B", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa13", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa14")
	if c := compute(t, e, "key-A", "G005-ITEM-A", []uint{a1, b1}); c.Code != http.StatusCreated {
		t.Fatalf("first compute failed: %d %s", c.Code, c.Body.String())
	}
	second := compute(t, e, "key-A", "G005-ITEM-B", []uint{a2, b2})
	if second.Code != http.StatusConflict {
		t.Fatalf("expected 409 reusing the same key with different inputs, got %d %s", second.Code, second.Body.String())
	}
}

func TestG005ComputeRepeatKeyReusedFlag(t *testing.T) {
	e := newTestEnv(t)
	a1, b1 := comparedPair(t, e, "G005-ITEM-C", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa15", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa16")
	if c := compute(t, e, "key-C", "G005-ITEM-C", []uint{a1, b1}); c.Code != http.StatusCreated {
		t.Fatalf("first compute failed: %d %s", c.Code, c.Body.String())
	}
	second := compute(t, e, "key-C", "G005-ITEM-C", []uint{a1, b1})
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 reusing the same key, got %d %s", second.Code, second.Body.String())
	}
	var resp struct {
		Data struct {
			Reused bool `json:"reused"`
		} `json:"data"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil || !resp.Data.Reused {
		t.Fatalf("expected reused=true on idempotent replay, got %s", second.Body.String())
	}
}

func TestG005ComputeRepeatInputReusedFlag(t *testing.T) {
	e := newTestEnv(t)
	a1, b1 := comparedPair(t, e, "G005-ITEM-D", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa17", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa18")
	if c := compute(t, e, "key-D1", "G005-ITEM-D", []uint{a1, b1}); c.Code != http.StatusCreated {
		t.Fatalf("first compute failed: %d %s", c.Code, c.Body.String())
	}
	second := compute(t, e, "key-D2", "G005-ITEM-D", []uint{a1, b1})
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 for repeated input with a new key, got %d %s", second.Code, second.Body.String())
	}
	var resp struct {
		Data struct {
			Reused bool `json:"reused"`
		} `json:"data"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil || !resp.Data.Reused {
		t.Fatalf("expected reused=true for repeated input, got %s", second.Body.String())
	}
}

func TestG005ComputeReusedStatusOK(t *testing.T) {
	e := newTestEnv(t)
	a1, b1 := comparedPair(t, e, "G005-ITEM-E", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa19", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa20")
	if c := compute(t, e, "key-E", "G005-ITEM-E", []uint{a1, b1}); c.Code != http.StatusCreated {
		t.Fatalf("first compute failed: %d %s", c.Code, c.Body.String())
	}
	second := compute(t, e, "key-E", "G005-ITEM-E", []uint{a1, b1})
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 (not 201) for idempotent replay, got %d %s", second.Code, second.Body.String())
	}
}

func TestG005EmptySnapshotNoPanic(t *testing.T) {
	e := newTestEnv(t)
	adjudicatorID := uint(5)
	caseRec := model.AdjudicationCase{
		DatasetID: e.datasetID, ItemKey: "G005-EMPTY", AnnotationSetIDsJSON: "[]",
		AgreementMetric: "cohen_kappa", AgreementScore: 0.5, ObservedAgreement: 0.8,
		ChanceAgreement: 0.4, SampleSize: 2, CoderCount: 2, MissingValueCount: 0,
		Applicability: "nominal", DisagreementType: "label",
		ConfusionSnapshotJSON: "[]", EvidenceSnapshotJSON: "[]", ClusterKey: "s:label:a~b",
		CaseState: "assigned", FinalLabelsJSON: "[]", Rationale: "",
		AdjudicatorID: &adjudicatorID,
		InputHash: "hash-empty", AlgorithmVersion: "v1",
		IdempotencyKey: "key-empty-snapshot", CreatedBy: 2,
	}
	if err := e.db.Create(&caseRec).Error; err != nil {
		t.Fatalf("insert case: %v", err)
	}
	system := service.NewSystemService(repository.NewSystemRepository(e.db), "test-secret-with-at-least-24-bytes-xyz", time.Hour)
	svc := service.NewAdjudicationCaseService(e.db, repository.NewAdjudicationCaseRepository(e.db), repository.NewAnnotationSetRepository(e.db), system, "v1")
	_, _, err := svc.Decide(caseRec.ID, dto.AdjudicateCaseRequest{
		FinalLabels: []dto.AnnotationLabel{{UnitKey: "document_class", Label: "RISK"}},
		Rationale:   "The disputed clause is a material risk because it shifts liability.",
	}, "empty-snapshot-key", dto.Actor{ID: 5, Username: "adjudicator", Role: "adjudicator"}, "req-empty")
	if err == nil {
		t.Fatalf("expected an error deciding a case with an empty annotation snapshot, got nil")
	}
}
