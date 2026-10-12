package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/shared/server/api"
)

func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) api.ErrorResponse {
	t.Helper()
	var body api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body
}

func runServiceError(t *testing.T, err error, log *slog.Logger) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinRequestID())
	r.GET("/", func(c *gin.Context) { writeServiceError(c, err, log) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

func TestWriteServiceError(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "user message wins over the wrap chain",
			err:        fmt.Errorf("lookup environment: %w", api.NotFound(`environment "stagin" not found in project p1`)),
			wantStatus: http.StatusNotFound,
			wantCode:   api.CodeNotFound,
			wantMsg:    `environment "stagin" not found in project p1`,
		},
		{
			name:       "bare sentinel keeps its wrap context",
			err:        fmt.Errorf("deployment d1 is running: %w", api.ErrConflict),
			wantStatus: http.StatusConflict,
			wantCode:   api.CodeConflict,
			wantMsg:    "deployment d1 is running: conflict",
		},
		{
			name:       "already exists",
			err:        api.AlreadyExists(`cluster "prod" already exists`),
			wantStatus: http.StatusConflict,
			wantCode:   api.CodeAlreadyExists,
			wantMsg:    `cluster "prod" already exists`,
		},
		{
			name:       "invalid input",
			err:        api.InvalidInput("parent environment e1 belongs to a different project"),
			wantStatus: http.StatusBadRequest,
			wantCode:   api.CodeInvalidInput,
			wantMsg:    "parent environment e1 belongs to a different project",
		},
		{
			name:       "forbidden",
			err:        api.Forbidden(`role "viewer" is read-only`),
			wantStatus: http.StatusForbidden,
			wantCode:   api.CodeForbidden,
			wantMsg:    `role "viewer" is read-only`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := runServiceError(t, tt.err, discard)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			body := decodeErrorBody(t, rec)
			if body.Code != tt.wantCode || body.Error != tt.wantMsg {
				t.Errorf("body = %+v, want code %q error %q", body, tt.wantCode, tt.wantMsg)
			}
			if body.RequestID == "" || body.RequestID != rec.Header().Get(requestIDHeader) {
				t.Errorf("requestId %q does not match header %q", body.RequestID, rec.Header().Get(requestIDHeader))
			}
		})
	}
}

func TestWriteServiceError_ValidationUsesCamelCaseFields(t *testing.T) {
	var ve api.ValidationErrors
	ve.Add("config", "name", "must not be empty")

	rec := runServiceError(t, &ve, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"errors":[{"component":"config","field":"name","message":"must not be empty"}]`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
	body := decodeErrorBody(t, rec)
	if body.Code != api.CodeValidationFailed || body.Error != "validation failed: config.name: must not be empty" {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestWriteServiceError_InternalHidesDetailsButLogsThem(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	rec := runServiceError(t, errors.New("pq: password authentication failed"), log)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decodeErrorBody(t, rec)
	if strings.Contains(body.Error, "pq:") {
		t.Errorf("internal error leaked to client: %q", body.Error)
	}
	if body.Code != api.CodeInternal || !strings.Contains(body.Error, body.RequestID) {
		t.Errorf("expected internal code and request ID in message, got %+v", body)
	}
	if !strings.Contains(logs.String(), "pq: password authentication failed") || !strings.Contains(logs.String(), body.RequestID) {
		t.Errorf("expected error and request ID in log, got %q", logs.String())
	}
}

const testJWTSecret = "router-test-secret"

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(RouterDeps{
		Authenticator: auth.NewJWTAuthenticator([]byte(testJWTSecret)),
		Authorizer:    auth.NewRoleAuthorizer(),
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func tokenFor(t *testing.T, role string) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"role": role}).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestRouter_ErrorResponses(t *testing.T) {
	router := testRouter(t)
	tests := []struct {
		name       string
		method     string
		path       string
		role       string
		body       string
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{"unknown route", http.MethodGet, "/api/v1/nope", "", "", http.StatusNotFound, api.CodeNotFound, "no API endpoint GET /api/v1/nope"},
		{"wrong method", http.MethodDelete, "/api/v1/projects", "admin", "", http.StatusMethodNotAllowed, api.CodeMethodNotAllowed, "method DELETE is not supported for /api/v1/projects"},
		{"missing token", http.MethodGet, "/api/v1/projects", "", "", http.StatusUnauthorized, api.CodeUnauthorized, `missing API token: send an "Authorization: Bearer <token>" header`},
		{"viewer writes", http.MethodPost, "/api/v1/projects", "viewer", `{"name":"x"}`, http.StatusForbidden, api.CodeForbidden, `role "viewer" is read-only and cannot write project resources`},
		{"member manages", http.MethodPost, "/api/v1/clusters", "member", `{"name":"x"}`, http.StatusForbidden, api.CodeForbidden, `role "member" cannot manage cluster resources; only admins can`},
		{"unknown body field", http.MethodPost, "/api/v1/projects", "admin", `{"nmae":"x"}`, http.StatusBadRequest, api.CodeInvalidInput, `unknown field "nmae" in request body; check the spelling, or whether the client and server versions differ`},
		{"empty body", http.MethodPost, "/api/v1/projects", "admin", "", http.StatusBadRequest, api.CodeInvalidInput, "request body is empty; expected a JSON object"},
		{"bad path id", http.MethodGet, "/api/v1/projects/abc", "admin", "", http.StatusBadRequest, api.CodeInvalidInput, `invalid project id "abc": expected a UUID`},
		{"bad token role", http.MethodPost, "/api/v1/tokens", "admin", `{"role":"owner"}`, http.StatusUnprocessableEntity, api.CodeValidationFailed, `validation failed: Token.role: must be one of admin, member or viewer, got "owner"`},
		{"bad status filter", http.MethodGet, "/api/v1/services/" + uuid.NewString() + "/deployments?status=done", "admin", "", http.StatusBadRequest, api.CodeInvalidInput, `invalid status "done": expected one of pending, running, succeeded, failed, cancelling, cancelled`},
		{"bad cursor", http.MethodGet, "/api/v1/services/" + uuid.NewString() + "/deployments?cursor=yesterday", "admin", "", http.StatusBadRequest, api.CodeInvalidInput, `invalid cursor "yesterday": pass the nextCursor value from a previous response`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.role != "" {
				req.Header.Set("Authorization", "Bearer "+tokenFor(t, tt.role))
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			body := decodeErrorBody(t, rec)
			if body.Code != tt.wantCode || body.Error != tt.wantMsg {
				t.Errorf("body = %+v\nwant code %q error %q", body, tt.wantCode, tt.wantMsg)
			}
			if body.RequestID == "" {
				t.Error("expected a request ID")
			}
		})
	}
}

func TestGinRecovery_ReturnsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinRequestID(), GinRecovery(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.GET("/boom", func(*gin.Context) { panic("nil map") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decodeErrorBody(t, rec)
	if body.Code != api.CodeInternal || body.RequestID == "" || strings.Contains(body.Error, "nil map") {
		t.Errorf("unexpected body: %+v", body)
	}
}
