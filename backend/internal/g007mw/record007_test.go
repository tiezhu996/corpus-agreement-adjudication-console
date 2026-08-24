package g007mw

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"corpus-annotation-agreement-control/backend/internal/middleware"
)

func TestG007PanicReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.Recovery())
	engine.GET("/boom", func(context *gin.Context) {
		panic("boom")
	})
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when a handler panics, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG007UnhandledErrorReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	engine.GET("/err", func(context *gin.Context) {
		context.Error(errors.New("boom"))
	})
	req := httptest.NewRequest(http.MethodGet, "/err", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for an unhandled error, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestG007RequestIDHeaderEchoed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.RequestID())
	engine.GET("/x", func(context *gin.Context) {
		context.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", "req-123")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected the response to echo X-Request-ID")
	}
}

func TestG007RateLimitedReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.RateLimit(1, "g007-test"))
	engine.GET("/rl", func(context *gin.Context) {
		context.String(http.StatusOK, "ok")
	})
	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/rl", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request should pass: %d", first.Code)
	}
	second := httptest.NewRecorder()
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/rl", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when rate limit is exceeded, got %d", second.Code)
	}
}

func TestG007CORSRejectsForeignOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.CORS("http://allowed.example"))
	engine.GET("/cors", func(context *gin.Context) {
		context.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/cors", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected no CORS header for a foreign origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestG007CORSAllowsConfiguredOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.CORS("http://allowed.example"))
	engine.GET("/cors", func(context *gin.Context) {
		context.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/cors", nil)
	req.Header.Set("Origin", "http://allowed.example")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://allowed.example" {
		t.Fatalf("expected CORS header for the configured origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestG007PreflightReturns204(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.CORS("http://allowed.example"))
	engine.POST("/preflight", func(context *gin.Context) {
		context.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodOptions, "/preflight", nil)
	req.Header.Set("Origin", "http://allowed.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for a CORS preflight request, got %d", rec.Code)
	}
}
