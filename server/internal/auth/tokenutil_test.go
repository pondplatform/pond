package auth

import (
	"net/http"
	"testing"
)

func TestBearerToken_MissingHeader(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	if got := BearerToken(r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestBearerToken_EmptyHeader(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "")
	if got := BearerToken(r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestBearerToken_MalformedNoParts(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "justoneword")
	if got := BearerToken(r); got != "" {
		t.Errorf("expected empty for missing scheme, got %q", got)
	}
}

func TestBearerToken_WrongScheme(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	if got := BearerToken(r); got != "" {
		t.Errorf("expected empty for Basic scheme, got %q", got)
	}
}

func TestBearerToken_ValidBearer(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer mytoken123")
	if got := BearerToken(r); got != "mytoken123" {
		t.Errorf("expected 'mytoken123', got %q", got)
	}
}

func TestBearerToken_CaseInsensitiveScheme(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "BEARER mytoken")
	if got := BearerToken(r); got != "mytoken" {
		t.Errorf("expected 'mytoken', got %q", got)
	}
}

func TestBearerToken_TokenWithSpaces(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	// Only splits on first space — token itself preserved
	r.Header.Set("Authorization", "Bearer tok en with spaces")
	if got := BearerToken(r); got != "tok en with spaces" {
		t.Errorf("expected full token after first space, got %q", got)
	}
}

func TestSHA256Hex_DeterministicOutput(t *testing.T) {
	a := SHA256Hex("my-secret")
	b := SHA256Hex("my-secret")
	if a != b {
		t.Error("SHA256Hex should be deterministic")
	}
}

func TestSHA256Hex_DifferentInputsDifferentOutputs(t *testing.T) {
	a := SHA256Hex("abc")
	b := SHA256Hex("xyz")
	if a == b {
		t.Error("expected different hashes for different inputs")
	}
}

func TestSHA256Hex_EmptyString(t *testing.T) {
	got := SHA256Hex("")
	if len(got) != 64 {
		t.Errorf("SHA256 hex should be 64 chars, got %d", len(got))
	}
}

func TestSHA256Hex_IsLowercaseHex(t *testing.T) {
	got := SHA256Hex("test")
	for _, c := range got {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("expected lowercase hex, got char %q in %q", c, got)
		}
	}
}
