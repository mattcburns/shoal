package ui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// sessionCookieName is the signed-cookie set on a successful /ui/login.
const sessionCookieName = "shoal_ui_session"

// sessionTTL is how long a login cookie stays valid.
const sessionTTL = 24 * time.Hour

// withUIAuth requires a valid session cookie for every /ui/* route when
// APIToken is set. /ui/login (both methods) and /ui/static/* stay open so the
// login page and its stylesheet are reachable pre-auth.
//
// Mirrors internal/api/auth.go's "auth disabled when token unset" behavior:
// an empty APIToken skips auth entirely (no login required), matching the
// lab-MVP default for the JSON API.
func (s *Server) withUIAuth(next http.Handler) http.Handler {
	token := strings.TrimSpace(s.apiToken)
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresUIAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookieName)
		if err != nil || !validSession(c.Value, token) {
			redirectToLogin(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requiresUIAuth(path string) bool {
	if path == "/ui/login" {
		return false
	}
	if strings.HasPrefix(path, "/ui/static/") {
		return false
	}
	return strings.HasPrefix(path, "/ui/")
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Path
	if r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	loc := "/ui/login"
	if next != "" && next != "/ui/login" {
		loc += "?next=" + url.QueryEscape(next)
	}
	http.Redirect(w, r, loc, http.StatusFound)
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.apiToken) == "" {
		http.Redirect(w, r, "/ui/devices", http.StatusFound)
		return
	}
	s.renderLogin(w, "", r.URL.Query().Get("next"))
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(s.apiToken)
	if token == "" {
		http.Redirect(w, r, "/ui/devices", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, "invalid form submission", "")
		return
	}
	next := r.FormValue("next")
	if !hmac.Equal([]byte(r.FormValue("password")), []byte(token)) {
		s.renderLogin(w, "incorrect password", next)
		return
	}
	value, err := newSessionCookieValue(token)
	if err != nil {
		s.log.Error("ui: create session cookie", "err", err.Error())
		s.renderLogin(w, "internal error", next)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(sessionTTL),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
	if strings.HasPrefix(next, "/ui/") {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/ui/devices", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/ui/login", http.StatusFound)
}

// loginTemplate parses login.html standalone (no layout.html/"content"
// wrapping): the login page has no nav/logout chrome to show pre-auth.
func loginTemplate() (*template.Template, error) {
	return template.ParseFS(templatesFS, "templates/login.html")
}

func (s *Server) renderLogin(w http.ResponseWriter, errMsg, next string) {
	tmpl, err := loginTemplate()
	if err != nil {
		s.log.Error("ui: parse login template", "err", err.Error())
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Error string
		Next  string
	}{Error: errMsg, Next: next}
	if err := tmpl.Execute(w, data); err != nil {
		s.log.Error("ui: execute login template", "err", err.Error())
	}
}

// newSessionCookieValue signs "<expiryUnix>|<nonceHex>" with HMAC-SHA256
// keyed by the API token, so a valid cookie can only be minted by someone who
// already knows the token (checked at login) -- forging one requires the key.
func newSessionCookieValue(token string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("ui: nonce: %w", err)
	}
	expiry := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%d|%s", expiry, hex.EncodeToString(nonce))
	sig := signPayload(payload, token)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func signPayload(payload, token string) []byte {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// csrfToken derives a per-session CSRF token from the request's own session
// cookie (HMAC-SHA256 keyed by the API token, over a "csrf|"-prefixed payload
// so this signature can never collide with a session cookie's own signature).
// Page handlers embed the result in a hidden form field on every state-
// mutating form; verifyCSRF checks it back on submit. Returns "" when auth is
// disabled (no session to protect) or the request has no session cookie yet
// (e.g. a direct hit on a POST route without ever loading the GET form --
// verifyCSRF then correctly rejects the empty token).
func (s *Server) csrfToken(r *http.Request) string {
	token := strings.TrimSpace(s.apiToken)
	if token == "" {
		return ""
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return hex.EncodeToString(signPayload("csrf|"+c.Value, token))
}

// verifyCSRF checks the request's csrf_token form field (constant-time)
// against csrfToken(r). Forms are only ever rendered to a request already
// holding a valid session cookie (withUIAuth), so a same-origin submit always
// carries a matching token; a cross-site forger who doesn't know the
// HttpOnly session cookie's value cannot compute it. Always true when auth is
// disabled (no session to forge against).
func (s *Server) verifyCSRF(r *http.Request) bool {
	if strings.TrimSpace(s.apiToken) == "" {
		return true
	}
	want := s.csrfToken(r)
	if want == "" {
		return false
	}
	return hmac.Equal([]byte(r.FormValue("csrf_token")), []byte(want))
}

// validSession verifies the cookie's HMAC (constant-time) and expiry.
func validSession(cookie, token string) bool {
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want := signPayload(string(payloadBytes), token)
	if !hmac.Equal(sig, want) {
		return false
	}
	fields := strings.SplitN(string(payloadBytes), "|", 2)
	if len(fields) != 2 {
		return false
	}
	expiry, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() <= expiry
}
