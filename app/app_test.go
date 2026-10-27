package app

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Tests run against a real Postgres database (TEST_DATABASE_URL), emptied before each test.

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://app:app@localhost:5432/controlfinan_go_test"
	}
	admin, err := pgxpool.New(ctx, strings.Replace(databaseURL, "/controlfinan_go_test", "/postgres", 1))
	if err == nil {
		admin.Exec(ctx, "CREATE DATABASE controlfinan_go_test")
		admin.Close()
	}
	testDB, err = pgxpool.New(ctx, databaseURL)
	if err != nil {
		panic(err)
	}
	if err := Migrate(ctx, testDB); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// fakeMailer keeps the sent e-mails.
type fakeMailer struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeMailer) Send(to, subject, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, to+"|"+subject+"|"+text)
	return nil
}

// clock lets a test move time forward.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type testApp struct {
	t      *testing.T
	server *httptest.Server
	app    *Server
	client *http.Client
	mailer *fakeMailer
	clock  *clock
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE sessions, current_accounts, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("America/Sao_Paulo")
	c := &clock{now: time.Date(2026, 10, 27, 12, 0, 0, 0, location)}
	mailer := &fakeMailer{}
	app, err := New(Config{DB: testDB, Secret: "test-secret", BcryptCost: 4, Mailer: mailer, Location: location, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	ta := &testApp{t: t, server: server, app: app, mailer: mailer, clock: c}
	ta.client = ta.newClient()
	return ta
}

func (ta *testApp) newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type response struct {
	status   int
	body     string
	location string
}

func (ta *testApp) get(path string) response {
	ta.t.Helper()
	return ta.do(ta.client, http.MethodGet, path, nil)
}

// post sends a form like the browser does, with the CSRF token from the cookie.
func (ta *testApp) post(path string, form url.Values) response {
	ta.t.Helper()
	return ta.postWith(ta.client, path, form)
}

func (ta *testApp) postWith(client *http.Client, path string, form url.Values) response {
	ta.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if form.Get("authenticity_token") == "" {
		token := ta.csrfToken(client)
		if token == "" {
			ta.do(client, http.MethodGet, "/passwords/new", nil) // any page with a form sets the CSRF cookie
			token = ta.csrfToken(client)
		}
		form.Set("authenticity_token", token)
	}
	return ta.do(client, http.MethodPost, path, form)
}

func (ta *testApp) csrfToken(client *http.Client) string {
	base, _ := url.Parse(ta.server.URL)
	for _, cookie := range client.Jar.Cookies(base) {
		if cookie.Name == csrfCookie {
			return cookie.Value
		}
	}
	return ""
}

func (ta *testApp) do(client *http.Client, method, path string, form url.Values) response {
	ta.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, ta.server.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := client.Do(req)
	if err != nil {
		ta.t.Fatal(err)
	}
	defer res.Body.Close()
	content, _ := io.ReadAll(res.Body)
	return response{res.StatusCode, string(content), strings.TrimPrefix(res.Header.Get("Location"), ta.server.URL)}
}

func (ta *testApp) createUser(email, password string) *User {
	ta.t.Helper()
	u := &User{EmailAddress: email}
	if err := u.SetPassword(password, 4); err != nil {
		ta.t.Fatal(err)
	}
	if err := CreateUser(context.Background(), testDB, u); err != nil {
		ta.t.Fatal(err)
	}
	return u
}

// signIn creates a user and signs in through the real login form.
func (ta *testApp) signIn() *User {
	ta.t.Helper()
	u := ta.createUser("driver@example.com", "secret123")
	res := ta.post("/session", url.Values{"email_address": {"driver@example.com"}, "password": {"secret123"}})
	if res.status != http.StatusFound {
		ta.t.Fatalf("sign in failed: %d", res.status)
	}
	return u
}

type entryAttrs struct {
	date     string
	cost     int
	kind     int
	cents    int64
	historic string
}

func (ta *testApp) createEntry(u *User, attrs entryAttrs) *Entry {
	ta.t.Helper()
	if attrs.date == "" {
		attrs.date = "2026-10-27"
	}
	if attrs.kind == 0 {
		attrs.kind = Credit
	}
	if attrs.cost == 0 && attrs.kind == Credit {
		attrs.cost = Rides
	}
	if attrs.cents == 0 {
		attrs.cents = 15000
	}
	day, _ := time.Parse("2006-01-02", attrs.date)
	e := &Entry{UserID: u.ID, CashAccountID: 5, CostID: attrs.cost, TypeLaunche: attrs.kind, Date: day, PriceCents: attrs.cents, PriceSet: true}
	if attrs.historic != "" {
		e.Historic = &attrs.historic
	}
	if err := SaveEntry(context.Background(), testDB, e); err != nil {
		ta.t.Fatal(err)
	}
	return e
}

func assertContains(t *testing.T, body string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(body, text) {
			t.Errorf("expected body to contain %q", text)
		}
	}
}

func assertNotContains(t *testing.T, body string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(body, text) {
			t.Errorf("expected body not to contain %q", text)
		}
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func assertInOrder(t *testing.T, body string, texts ...string) {
	t.Helper()
	pattern := ""
	for _, text := range texts {
		pattern += "(?s).*" + regexp.QuoteMeta(text)
	}
	if !regexp.MustCompile(pattern).MatchString(body) {
		t.Errorf("expected body to contain in order: %q", texts)
	}
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testDB.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
