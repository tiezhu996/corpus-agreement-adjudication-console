package g003state

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
	adjudicatorToken string
	reviewerToken    string
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
	e := &env{engine: engine, db: db}
	e.adjudicatorToken = login(t, engine, "adjudicator", "Decide#536")
	e.reviewerToken = login(t, engine, "reviewer", "Review#536")
	return e
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

func insertCase(t *testing.T, e *env, state string, adjudicatorID, reviewedBy *uint, tag string) uint {
	t.Helper()
	var annotations []model.AnnotationSet
	if err := e.db.Find(&annotations).Error; err != nil || len(annotations) < 2 {
		t.Fatalf("seeded annotations unavailable: %v", err)
	}
	ids := []uint{annotations[0].ID, annotations[1].ID}
	idsJSON, _ := json.Marshal(ids)
	rec := model.AdjudicationCase{
		DatasetID: annotations[0].DatasetID, ItemKey: "G003-ITEM-" + tag,
		AnnotationSetIDsJSON: string(idsJSON), AgreementMetric: "cohen_kappa",
		AgreementScore: 0.5, ObservedAgreement: 0.8, ChanceAgreement: 0.4,
		SampleSize: 2, CoderCount: 2, MissingValueCount: 0,
		Applicability: "nominal", DisagreementType: "label",
		ConfusionSnapshotJSON: "[]", EvidenceSnapshotJSON: "[]",
		ClusterKey: "s:label:a~b", CaseState: state, FinalLabelsJSON: "[]",
		Rationale: "", AdjudicatorID: adjudicatorID, ReviewedBy: reviewedBy,
		InputHash: "hash-" + state + "-" + tag, AlgorithmVersion: "v1",
		IdempotencyKey: "key-" + state + "-" + tag, CreatedBy: 2,
	}
	if err := e.db.Create(&rec).Error; err != nil {
		t.Fatalf("insert case: %v", err)
	}
	return rec.ID
}

func postJSON(t *testing.T, e *env, token, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	return rec
}

func TestG003ReviewedCaseCanAccept(t *testing.T) {
	e := newTestEnv(t)
	reviewerID := uint(6)
	id := insertCase(t, e, "reviewed", nil, &reviewerID, "accept")
	rec := postJSON(t, e, e.reviewerToken, fmt.Sprintf("/api/v1/adjudications/%d/accept", id), map[string]any{"note": "Independent review accepted."})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 accepting a reviewed case, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			CaseState string `json:"case_state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.CaseState != "accepted" {
		t.Fatalf("case was not accepted: %s", rec.Body.String())
	}
}

func TestG003ReopenIncrementsCount(t *testing.T) {
	e := newTestEnv(t)
	reviewerID := uint(6)
	id := insertCase(t, e, "reviewed", nil, &reviewerID, "reopen")
	rec := postJSON(t, e, e.reviewerToken, fmt.Sprintf("/api/v1/adjudications/%d/reopen", id), map[string]any{"note": "Needs another look."})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 reopening a reviewed case, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ReopenCount int `json:"reopen_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ReopenCount != 1 {
		t.Fatalf("expected reopen_count 1, got %s", rec.Body.String())
	}
}

func TestG003ReviewRecordsReviewer(t *testing.T) {
	e := newTestEnv(t)
	adjudicatorID := uint(5)
	id := insertCase(t, e, "adjudicated", &adjudicatorID, nil, "review")
	rec := postJSON(t, e, e.reviewerToken, fmt.Sprintf("/api/v1/adjudications/%d/review", id), map[string]any{"note": "Independent review completed."})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 reviewing an adjudicated case, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ReviewedBy *uint `json:"reviewed_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ReviewedBy == nil || *resp.Data.ReviewedBy != 6 {
		t.Fatalf("expected reviewed_by to be the reviewer, got %s", rec.Body.String())
	}
}

func TestG003DecideRecordsTime(t *testing.T) {
	e := newTestEnv(t)
	adjudicatorID := uint(5)
	id := insertCase(t, e, "assigned", &adjudicatorID, nil, "decide")
	payload := map[string]any{
		"final_labels": []map[string]any{{"unit_key": "document_class", "label": "RISK"}},
		"rationale":    "The disputed clause is a material risk because it shifts liability.",
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/adjudications/%d/decide", id), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.adjudicatorToken)
	req.Header.Set("Idempotency-Key", "decide-key-"+fmt.Sprint(id))
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 deciding an assigned case, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			DecidedAt *time.Time `json:"decided_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.DecidedAt == nil {
		t.Fatalf("expected decided_at to be recorded, got %s", rec.Body.String())
	}
}

func TestG003AssignRequiresOpenState(t *testing.T) {
	e := newTestEnv(t)
	adjudicatorID := uint(5)
	id := insertCase(t, e, "assigned", &adjudicatorID, nil, "assign-guard")
	rec := postJSON(t, e, e.reviewerToken, fmt.Sprintf("/api/v1/adjudications/%d/assign", id), map[string]any{"note": "Attempt to claim an assigned case."})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 assigning an assigned case, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG003ReviewSucceeds(t *testing.T) {
	e := newTestEnv(t)
	adjudicatorID := uint(5)
	id := insertCase(t, e, "adjudicated", &adjudicatorID, nil, "review-edge")
	rec := postJSON(t, e, e.reviewerToken, fmt.Sprintf("/api/v1/adjudications/%d/review", id), map[string]any{"note": "Independent review completed."})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 reviewing an adjudicated case, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG003ReviewedCaseCanReopen(t *testing.T) {
	e := newTestEnv(t)
	reviewerID := uint(6)
	id := insertCase(t, e, "reviewed", nil, &reviewerID, "reopen-edge")
	rec := postJSON(t, e, e.reviewerToken, fmt.Sprintf("/api/v1/adjudications/%d/reopen", id), map[string]any{"note": "Reopen for another look."})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 reopening a reviewed case, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG003DecideMovesToAdjudicated(t *testing.T) {
	e := newTestEnv(t)
	adjudicatorID := uint(5)
	id := insertCase(t, e, "assigned", &adjudicatorID, nil, "decide-state")
	payload := map[string]any{
		"final_labels": []map[string]any{{"unit_key": "document_class", "label": "RISK"}},
		"rationale":    "The disputed clause is a material risk because it shifts liability.",
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/adjudications/%d/decide", id), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.adjudicatorToken)
	req.Header.Set("Idempotency-Key", "decide-state-key-"+fmt.Sprint(id))
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 deciding an assigned case, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			CaseState string `json:"case_state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.CaseState != "adjudicated" {
		t.Fatalf("expected case state adjudicated after decide, got %s", rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
