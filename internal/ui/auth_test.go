package ui

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidSession(t *testing.T) {
	const token = "s3cr3t-token"

	value, err := newSessionCookieValue(token)
	if err != nil {
		t.Fatalf("newSessionCookieValue: %v", err)
	}
	if !validSession(value, token) {
		t.Fatal("freshly minted cookie should be valid")
	}
	if validSession(value, "wrong-token") {
		t.Fatal("cookie must not validate against a different token")
	}
	if validSession("garbage", token) {
		t.Fatal("malformed cookie must not validate")
	}
	if validSession("", token) {
		t.Fatal("empty cookie must not validate")
	}

	// Tamper with the payload half without recomputing the signature.
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected cookie shape: %q", value)
	}
	tamperedPayload := base64.RawURLEncoding.EncodeToString([]byte("9999999999|deadbeef"))
	tampered := tamperedPayload + "." + parts[1]
	if validSession(tampered, token) {
		t.Fatal("tampered payload must fail signature check")
	}

	// Expired payload, correctly signed.
	expiredPayload := "0|deadbeefdeadbeef"
	sig := signPayload(expiredPayload, token)
	expired := base64.RawURLEncoding.EncodeToString([]byte(expiredPayload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
	if validSession(expired, token) {
		t.Fatal("expired cookie must not validate")
	}
}

func TestWithUIAuth(t *testing.T) {
	const token = "s3cr3t-token"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("disabled when token empty", func(t *testing.T) {
		s := &Server{log: testLogger(), apiToken: ""}
		h := s.withUIAuth(inner)
		req := httptest.NewRequest(http.MethodGet, "/ui/devices", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200 (auth disabled)", rec.Code)
		}
	})

	t.Run("redirects without cookie", func(t *testing.T) {
		s := &Server{log: testLogger(), apiToken: token}
		h := s.withUIAuth(inner)
		req := httptest.NewRequest(http.MethodGet, "/ui/devices", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("got %d, want 302 redirect to login", rec.Code)
		}
		if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/ui/login") {
			t.Fatalf("Location = %q, want /ui/login prefix", loc)
		}
	})

	t.Run("login and static stay open", func(t *testing.T) {
		s := &Server{log: testLogger(), apiToken: token}
		h := s.withUIAuth(inner)
		for _, p := range []string{"/ui/login", "/ui/static/style.css"} {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: got %d, want 200", p, rec.Code)
			}
		}
	})

	t.Run("valid cookie passes through", func(t *testing.T) {
		s := &Server{log: testLogger(), apiToken: token}
		h := s.withUIAuth(inner)
		value, err := newSessionCookieValue(token)
		if err != nil {
			t.Fatalf("newSessionCookieValue: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/ui/devices", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: value})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
	})
}

func TestLoginSubmitSetsCookieAndRedirects(t *testing.T) {
	const token = "s3cr3t-token"
	s := New(Config{APIToken: token, Log: testLogger()})

	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader("password="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rec.Code)
	}
	resp := rec.Result()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("expected a session cookie to be set")
	}
	if !validSession(cookie.Value, token) {
		t.Fatal("issued cookie should itself validate")
	}
}

func TestLoginSubmitWrongPassword(t *testing.T) {
	const token = "s3cr3t-token"
	s := New(Config{APIToken: token, Log: testLogger()})

	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader("password=nope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (re-render login with error)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "incorrect password") {
		t.Fatalf("body missing error message: %s", rec.Body.String())
	}
}
