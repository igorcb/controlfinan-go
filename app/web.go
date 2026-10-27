package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie  = "session_id"
	csrfCookie     = "_csrf_token"
	flashCookie    = "_flash"
	returnToCookie = "return_to"
	permanentAge   = 20 * 365 * 24 * 60 * 60 // cookies.permanent
)

func itoa(n int) string { return strconv.Itoa(n) }

// sign and verify protect cookie values with HMAC-SHA256 ("value--signature").
func (s *Server) sign(purpose, value string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(purpose + ":" + value))
	return base64.RawURLEncoding.EncodeToString([]byte(value)) + "--" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) verify(purpose, signed string) (string, bool) {
	encoded, signature, found := strings.Cut(signed, "--")
	if !found {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	expected := s.sign(purpose, string(raw))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(encoded+"--"+signature)) != 1 {
		return "", false
	}
	return string(raw), true
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) deleteCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) readSigned(r *http.Request, name string) (string, bool) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", false
	}
	return s.verify(name, cookie.Value)
}

// Authentication like Rails 8's generator: a signed "session_id" cookie pointing to a row in `sessions`.

type contextKey int

const userKey contextKey = 1

type current struct {
	SessionID int64
	User      *User
}

func currentOf(r *http.Request) *current {
	c, _ := r.Context().Value(userKey).(*current)
	return c
}

func currentUser(r *http.Request) *User {
	if c := currentOf(r); c != nil {
		return c.User
	}
	return nil
}

func (s *Server) resumeSession(r *http.Request) *http.Request {
	value, ok := s.readSigned(r, sessionCookie)
	if !ok {
		return r
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return r
	}
	c := &current{SessionID: id, User: &User{}}
	err = s.db.QueryRow(r.Context(), `SELECT u.id, u.email_address, u.password_digest FROM sessions s
		JOIN users u ON u.id = s.user_id WHERE s.id = $1`, id).Scan(&c.User.ID, &c.User.EmailAddress, &c.User.PasswordDigest)
	if err != nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), userKey, c))
}

func (s *Server) startNewSessionFor(w http.ResponseWriter, r *http.Request, u *User) error {
	userAgent := r.UserAgent()
	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}
	var id int64
	if err := s.db.QueryRow(r.Context(), `INSERT INTO sessions (user_id, user_agent, ip_address, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now()) RETURNING id`, u.ID, userAgent, remoteIP(r)).Scan(&id); err != nil {
		return err
	}
	s.setCookie(w, sessionCookie, s.sign(sessionCookie, strconv.FormatInt(id, 10)), permanentAge)
	return nil
}

func (s *Server) terminateSession(w http.ResponseWriter, r *http.Request) error {
	if c := currentOf(r); c != nil {
		if _, err := s.db.Exec(r.Context(), "DELETE FROM sessions WHERE id = $1", c.SessionID); err != nil {
			return err
		}
	}
	s.deleteCookie(w, sessionCookie)
	return nil
}

// requireAuthentication redirects guests to the login and remembers the page they asked for.
func (s *Server) requireAuthentication(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r) == nil {
			s.setCookie(w, returnToCookie, s.sign(returnToCookie, absoluteURL(r, r.URL.RequestURI())), 0)
			http.Redirect(w, r, "/session/new", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) afterAuthenticationURL(w http.ResponseWriter, r *http.Request) string {
	if url, ok := s.readSigned(r, returnToCookie); ok {
		s.deleteCookie(w, returnToCookie)
		return url
	}
	return absoluteURL(r, "/")
}

func absoluteURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CSRF protection with a random token kept in a cookie and repeated in every form (double submit).
// render calls csrfToken once per page, so every form on the page gets the same token.

func (s *Server) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	bytes := make([]byte, 32)
	rand.Read(bytes)
	token := base64.RawURLEncoding.EncodeToString(bytes)
	s.setCookie(w, csrfCookie, token, 0)
	return token
}

func validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.PostFormValue("authenticity_token"))) == 1
}

// Flash messages survive one redirect, like Rails' flash.

type flash struct {
	Notice string `json:"notice,omitempty"`
	Alert  string `json:"alert,omitempty"`
}

func (s *Server) setFlash(w http.ResponseWriter, f flash) {
	data, _ := json.Marshal(f)
	s.setCookie(w, flashCookie, s.sign(flashCookie, string(data)), 0)
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) flash {
	var f flash
	value, ok := s.readSigned(r, flashCookie)
	if !ok {
		return f
	}
	json.Unmarshal([]byte(value), &f)
	s.deleteCookie(w, flashCookie)
	return f
}

func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, url string, status int, f flash) {
	s.setFlash(w, f)
	http.Redirect(w, r, url, status)
}

// rateLimiter works like Rails `rate_limit to: 10, within: 3.minutes`: a counter per action and IP.
type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateEntry
	now     func() time.Time
}

type rateEntry struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(now func() time.Time) *rateLimiter {
	return &rateLimiter{entries: map[string]*rateEntry{}, now: now}
}

func (l *rateLimiter) allow(key string, to int, within time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	entry, found := l.entries[key]
	if !found || now.After(entry.resetAt) {
		entry = &rateEntry{resetAt: now.Add(within)}
		l.entries[key] = entry
	}
	entry.count++
	return entry.count <= to
}

func (s *Server) rateLimit(name, backTo string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(name+":"+remoteIP(r), 10, 3*time.Minute) {
			s.redirectWith(w, r, backTo, http.StatusFound, flash{Alert: "Tente novamente mais tarde."})
			return
		}
		next(w, r)
	}
}
