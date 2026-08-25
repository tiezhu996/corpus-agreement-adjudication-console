package g006auth

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
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"corpus-annotation-agreement-control/backend/internal/config"
	"corpus-annotation-agreement-control/backend/internal/model"
	"corpus-annotation-agreement-control/backend/internal/router"
)

const testSecret = "test-secret-with-at-least-24-bytes-xyz"

func newTestEnv(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		Port: "0", DBDriver: "sqlite",
		DBDSN:              fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()),
		DBAutoMigrate:      true,
		JWTSecret:          testSecret,
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
	return router.New(db, cfg), db
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

func TestG006LoginTokenUsable(t *testing.T) {
	engine, _ := newTestEnv(t)
	rec := login(t, engine, "manager", "Data#536")
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	token := tokenOf(t, rec)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/datasets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, req)
	if out.Code != http.StatusOK {
		t.Fatalf("expected the login token to be usable, got %d %s", out.Code, out.Body.String())
	}
}

func TestG006LoginResponseRoleCorrect(t *testing.T) {
	engine, _ := newTestEnv(t)
	rec := login(t, engine, "manager", "Data#536")
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			User struct {
				Role string `json:"role"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.User.Role != "data_manager" {
		t.Fatalf("expected login to report the real role data_manager, got %s", rec.Body.String())
	}
}

func TestG006AuditFailureRollsBack(t *testing.T) {
	engine, db := newTestEnv(t)
	callbackName := "test:fail-audit-" + t.Name()
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "AuditEvent" {
			tx.AddError(errors.New("injected audit storage failure"))
		}
	}); err != nil {
		t.Fatalf("register audit failure callback: %v", err)
	}
	token := tokenOf(t, login(t, engine, "manager", "Data#536"))
	payload, _ := json.Marshal(map[string]any{
		"dataset_code": "AUDIT-RB-1", "name": "Rollback corpus", "language": "en",
		"domain": "quality", "document_count": 1,
		"content_mask_policy": "Mask all identifiers before review.", "owner_team": "Quality",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the audit write fails, got %d %s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.Model(&model.CorpusDataset{}).Where("dataset_code = ?", "AUDIT-RB-1").Count(&count).Error; err != nil {
		t.Fatalf("count datasets: %v", err)
	}
	if count != 0 {
		t.Fatalf("dataset survived a failed audit write: count=%d", count)
	}
}

func TestG006MissingExpirationTokenRejected(t *testing.T) {
	engine, _ := newTestEnv(t)
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": 2, "username": "manager", "role": "data_manager",
		"sub": "2", "iss": "corpus-agreement-api", "iat": now.Unix(),
	}).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/datasets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token without expiration, got %d", rec.Code)
	}
}

func TestG006ManagerRolePreserved(t *testing.T) {
	engine, _ := newTestEnv(t)
	token := tokenOf(t, login(t, engine, "manager", "Data#536"))
	payload, _ := json.Marshal(map[string]any{
		"dataset_code": "ROLE-OK-1", "name": "Manager corpus", "language": "en",
		"domain": "quality", "document_count": 1,
		"content_mask_policy": "Mask all identifiers before review.", "owner_team": "Quality",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating a dataset as manager, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG006AuditRequestIDRecorded(t *testing.T) {
	engine, _ := newTestEnv(t)
	token := tokenOf(t, login(t, engine, "manager", "Data#536"))
	payload, _ := json.Marshal(map[string]any{
		"dataset_code": "REQID-1", "name": "Request id corpus", "language": "en",
		"domain": "quality", "document_count": 1,
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
	auditor := tokenOf(t, login(t, engine, "auditor", "Audit#536"))
	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/audit?action=corpus_dataset.created", nil)
	auditReq.Header.Set("Authorization", "Bearer "+auditor)
	auditRec := httptest.NewRecorder()
	engine.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("audit list failed: %d %s", auditRec.Code, auditRec.Body.String())
	}
	var resp struct {
		Data []struct {
			RequestID string `json:"request_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(auditRec.Body.Bytes(), &resp); err != nil || len(resp.Data) == 0 {
		t.Fatalf("audit response malformed: %s", auditRec.Body.String())
	}
	for _, item := range resp.Data {
		if item.RequestID == "" {
			t.Fatal("expected audit events to carry a request id")
		}
	}
}

func TestG006ForeignMethodTokenRejected(t *testing.T) {
	engine, _ := newTestEnv(t)
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"user_id": 2, "username": "manager", "role": "data_manager",
		"sub": "2", "iss": "corpus-agreement-api", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/datasets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token signed with a foreign method, got %d", rec.Code)
	}
}
