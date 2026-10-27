package app

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"crypto/hmac"
	"crypto/sha256"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static
var staticFiles embed.FS

const perPage = 30

var months = []string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

// Mailer delivers the password reset e-mail. The default one writes it to the log.
type Mailer interface {
	Send(to, subject, text string) error
}

type LogMailer struct{}

func (LogMailer) Send(to, subject, text string) error {
	log.Printf("mail to=%s subject=%q\n%s", to, subject, text)
	return nil
}

type Config struct {
	DB         *pgxpool.Pool
	Secret     string
	BcryptCost int
	Mailer     Mailer
	Location   *time.Location
	Now        func() time.Time
}

type Server struct {
	db         *pgxpool.Pool
	secret     []byte
	bcryptCost int
	mailer     Mailer
	location   *time.Location
	now        func() time.Time
	limiter    *rateLimiter
	pages      map[string]*template.Template
	mux        *http.ServeMux
	// dummy is checked when the e-mail does not exist, so both cases take the same time.
	dummy *User
}

// Migrate creates the tables (same schema as the Rails app) when they do not exist.
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, schema)
	return err
}

func New(config Config) (*Server, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Location == nil {
		config.Location = time.Local
	}
	if config.Mailer == nil {
		config.Mailer = LogMailer{}
	}
	s := &Server{
		db: config.DB, secret: []byte(config.Secret), bcryptCost: config.BcryptCost, mailer: config.Mailer,
		location: config.Location, now: config.Now, limiter: newRateLimiter(config.Now), pages: map[string]*template.Template{},
	}
	s.dummy = &User{}
	if err := s.dummy.SetPassword("dummy-password", s.bcryptCost); err != nil {
		return nil, err
	}
	funcs := template.FuncMap{"currency": Currency, "amountClass": AmountClass, "table": amountsTable}
	pages, err := fs.Glob(templateFiles, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		name := strings.TrimSuffix(strings.TrimPrefix(page, "templates/"), ".html")
		if name == "layout" || strings.HasPrefix(name, "_") {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFiles, "templates/layout.html", "templates/_*.html", page)
		if err != nil {
			return nil, err
		}
		s.pages[name] = t
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	m := http.NewServeMux()
	static, _ := fs.Sub(staticFiles, "static")
	files := http.FileServerFS(static)
	for _, path := range []string{"GET /css/", "GET /js/", "GET /icon.png", "GET /icon.svg"} {
		m.Handle(path, files)
	}
	m.HandleFunc("GET /up", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><html><body style="background-color: green"></body></html>`)
	})

	m.HandleFunc("GET /session/new", s.newSession)
	m.HandleFunc("POST /session", s.rateLimit("sessions", "/session/new", s.createSession))
	m.HandleFunc("DELETE /session", s.requireAuthentication(s.destroySession))
	m.HandleFunc("GET /registration/new", s.newRegistration)
	m.HandleFunc("POST /registration", s.rateLimit("registrations", "/registration/new", s.createRegistration))
	m.HandleFunc("GET /passwords/new", s.newPassword)
	m.HandleFunc("POST /passwords", s.rateLimit("passwords", "/passwords/new", s.createPassword))
	m.HandleFunc("GET /passwords/{token}/edit", s.editPassword)
	m.HandleFunc("PUT /passwords/{token}", s.updatePassword)
	m.HandleFunc("PATCH /passwords/{token}", s.updatePassword)

	m.HandleFunc("GET /{$}", s.requireAuthentication(s.listEntries))
	m.HandleFunc("GET /current_accounts", s.requireAuthentication(s.listEntries))
	m.HandleFunc("GET /current_accounts.json", s.requireAuthentication(s.listEntries))
	m.HandleFunc("POST /current_accounts", s.requireAuthentication(s.createEntry))
	m.HandleFunc("GET /current_accounts/new", s.requireAuthentication(s.newEntry))
	m.HandleFunc("GET /current_accounts/{id}", s.requireAuthentication(s.showEntry))
	m.HandleFunc("GET /current_accounts/{id}/edit", s.requireAuthentication(s.editEntry))
	m.HandleFunc("PUT /current_accounts/{id}", s.requireAuthentication(s.updateEntry))
	m.HandleFunc("PATCH /current_accounts/{id}", s.requireAuthentication(s.updateEntry))
	m.HandleFunc("DELETE /current_accounts/{id}", s.requireAuthentication(s.destroyEntry))
	m.HandleFunc("GET /dashboard", s.requireAuthentication(s.showDashboard))

	// Legacy daily balance search, now a filter on the listing.
	m.HandleFunc("GET /search_account", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/current_accounts?date="+r.URL.Query().Get("date_calendar_now"), http.StatusMovedPermanently)
	})
	s.mux = m
}

// ServeHTTP applies the Rails-like request pipeline: method override, CSRF check and session.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if method := strings.ToUpper(r.PostFormValue("_method")); method == "PUT" || method == "PATCH" || method == "DELETE" {
			r.Method = method
		}
		if !validCSRF(r) {
			http.Error(w, "Unprocessable Content", http.StatusUnprocessableEntity)
			return
		}
	}
	s.mux.ServeHTTP(w, s.resumeSession(r))
}

func (s *Server) today() time.Time {
	now := s.now().In(s.location)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// render executes a page inside the layout. data is the page's own values.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, status int, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	f := s.takeFlash(w, r)
	data["User"] = currentUser(r)
	data["Notice"] = f.Notice
	data["Alert"] = f.Alert
	data["CSRF"] = s.csrfToken(w, r)
	var body bytes.Buffer
	if err := s.pages[page].Execute(&body, data); err != nil {
		s.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body.Bytes())
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

// Compact JSON like jbuilder (no HTML escaping, no trailing newline).
func writeJSON(w http.ResponseWriter, value any) {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	encoder.Encode(value)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(bytes.TrimRight(body.Bytes(), "\n"))
}

func parseDate(value string) *time.Time {
	day, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil
	}
	return &day
}

// Sessions

func (s *Server) newSession(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "sessions_new", http.StatusOK, map[string]any{"EmailAddress": r.URL.Query().Get("email_address")})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	u, err := FindUserByEmail(r.Context(), s.db, r.PostFormValue("email_address"))
	if err != nil {
		s.serverError(w, err)
		return
	}
	if u == nil {
		// Burn the same time as a real check to avoid user enumeration by timing.
		s.dummy.CheckPassword("x")
	}
	if u == nil || !u.CheckPassword(r.PostFormValue("password")) {
		s.redirectWith(w, r, "/session/new", http.StatusFound, flash{Alert: "E-mail ou senha inválidos."})
		return
	}
	if err := s.startNewSessionFor(w, r, u); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, s.afterAuthenticationURL(w, r), http.StatusFound)
}

func (s *Server) destroySession(w http.ResponseWriter, r *http.Request) {
	if err := s.terminateSession(w, r); err != nil {
		s.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/session/new", http.StatusSeeOther)
}

// Registrations

func (s *Server) newRegistration(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "registrations_new", http.StatusOK, map[string]any{"EmailAddress": ""})
}

func (s *Server) createRegistration(w http.ResponseWriter, r *http.Request) {
	u := &User{EmailAddress: r.PostFormValue("user[email_address]")}
	password := r.PostFormValue("user[password]")
	confirmation := r.PostFormValue("user[password_confirmation]")
	if errs := ValidateUser(r.Context(), s.db, u, &password, &confirmation, true); len(errs) > 0 {
		s.render(w, r, "registrations_new", http.StatusUnprocessableEntity, map[string]any{"EmailAddress": u.EmailAddress, "Errors": errs})
		return
	}
	if err := u.SetPassword(password, s.bcryptCost); err != nil {
		s.serverError(w, err)
		return
	}
	if err := CreateUser(r.Context(), s.db, u); err != nil {
		s.serverError(w, err)
		return
	}
	if err := s.startNewSessionFor(w, r, u); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirectWith(w, r, "/", http.StatusFound, flash{Notice: "Conta criada."})
}

// Passwords: the reset token is signed, expires in 15 minutes and stops working once the password changes.

const passwordResetMaxAge = 15 * time.Minute

func (s *Server) passwordResetToken(u *User) string {
	payload := fmt.Sprintf("%d:%s:%d", u.ID, u.PasswordDigest[len(u.PasswordDigest)-10:], s.now().Add(passwordResetMaxAge).Unix())
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("password_reset:" + payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) findByPasswordResetToken(ctx context.Context, token string) *User {
	encoded, signature, found := strings.Cut(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if !found || err != nil {
		return nil
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("password_reset:" + string(payload)))
	if !hmac.Equal([]byte(base64.RawURLEncoding.EncodeToString(mac.Sum(nil))), []byte(signature)) {
		return nil
	}
	parts := strings.Split(string(payload), ":")
	if len(parts) != 3 {
		return nil
	}
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	expires, _ := strconv.ParseInt(parts[2], 10, 64)
	if s.now().Unix() > expires {
		return nil
	}
	u, err := FindUser(ctx, s.db, id)
	if err != nil || u == nil || u.PasswordDigest[len(u.PasswordDigest)-10:] != parts[1] {
		return nil
	}
	return u
}

func (s *Server) newPassword(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "passwords_new", http.StatusOK, map[string]any{"EmailAddress": r.URL.Query().Get("email_address")})
}

func (s *Server) createPassword(w http.ResponseWriter, r *http.Request) {
	u, err := FindUserByEmail(r.Context(), s.db, r.PostFormValue("email_address"))
	if err != nil {
		s.serverError(w, err)
		return
	}
	if u != nil {
		url := absoluteURL(r, "/passwords/"+s.passwordResetToken(u)+"/edit")
		text := "Você pode redefinir sua senha em\n" + url + "\n\nEste link expira em 15 minutos.\n"
		if err := s.mailer.Send(u.EmailAddress, "Redefinição de senha", text); err != nil {
			log.Printf("mail error: %v", err)
		}
	}
	s.redirectWith(w, r, "/session/new", http.StatusFound,
		flash{Notice: "Se o e-mail estiver cadastrado, enviamos as instruções para redefinir a senha."})
}

func (s *Server) editPassword(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if s.findByPasswordResetToken(r.Context(), token) == nil {
		s.redirectWith(w, r, "/passwords/new", http.StatusFound, flash{Alert: "O link de redefinição é inválido ou expirou."})
		return
	}
	s.render(w, r, "passwords_edit", http.StatusOK, map[string]any{"Token": token})
}

func (s *Server) updatePassword(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	u := s.findByPasswordResetToken(r.Context(), token)
	if u == nil {
		s.redirectWith(w, r, "/passwords/new", http.StatusFound, flash{Alert: "O link de redefinição é inválido ou expirou."})
		return
	}
	password := r.PostFormValue("password")
	confirmation := r.PostFormValue("password_confirmation")
	if errs := ValidateUser(r.Context(), s.db, u, &password, &confirmation, true); len(errs) > 0 {
		s.redirectWith(w, r, "/passwords/"+token+"/edit", http.StatusFound, flash{Alert: "As senhas não conferem ou têm menos de 6 caracteres."})
		return
	}
	if err := u.SetPassword(password, s.bcryptCost); err != nil {
		s.serverError(w, err)
		return
	}
	if err := UpdatePassword(r.Context(), s.db, u); err != nil {
		s.serverError(w, err)
		return
	}
	if _, err := s.db.Exec(r.Context(), "DELETE FROM sessions WHERE user_id = $1", u.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirectWith(w, r, "/session/new", http.StatusFound, flash{Notice: "Senha redefinida."})
}

// Current accounts (entries)

type entryJSON struct {
	ID            int64   `json:"id"`
	CashAccountID int     `json:"cash_account_id"`
	CostID        int     `json:"cost_id"`
	DateOcurrence string  `json:"date_ocurrence"`
	Price         string  `json:"price"`
	Historic      *string `json:"historic"`
	UserID        int64   `json:"user_id"`
	TypeLaunche   int     `json:"type_launche"`
	URL           string  `json:"url"`
}

func asJSON(r *http.Request, e *Entry) entryJSON {
	return entryJSON{e.ID, e.CashAccountID, e.CostID, e.DateISO(), DecimalText(e.PriceCents), e.Historic, e.UserID, e.TypeLaunche,
		absoluteURL(r, "/current_accounts/"+strconv.FormatInt(e.ID, 10)+".json")}
}

func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	query := r.URL.Query()
	day := parseDate(query.Get("date"))
	page, _ := strconv.Atoi(query.Get("page"))
	page = max(page, 1)
	entries, err := ListEntries(r.Context(), s.db, u.ID, day, perPage+1, (page-1)*perPage)
	if err != nil {
		s.serverError(w, err)
		return
	}
	nextPage := len(entries) > perPage
	if nextPage {
		entries = entries[:perPage]
	}
	if strings.HasSuffix(r.URL.Path, ".json") {
		list := make([]entryJSON, 0, len(entries))
		for _, e := range entries {
			list = append(list, asJSON(r, e))
		}
		writeJSON(w, list)
		return
	}
	balance, err := Balance(r.Context(), s.db, u.ID, day)
	if err != nil {
		s.serverError(w, err)
		return
	}
	dateParam, dateLabel, dateValue := "", "", ""
	if day != nil {
		dateValue = day.Format("2006-01-02")
		dateParam = "date=" + dateValue + "&"
		dateLabel = day.Format("02/01/2006")
	}
	s.render(w, r, "current_accounts_index", http.StatusOK, map[string]any{
		"Nav": "index", "Entries": entries, "Balance": balance, "DateLabel": dateLabel, "DateValue": dateValue,
		"DateParam": dateParam, "Page": page, "PreviousPage": page - 1, "NextPageNumber": page + 1, "NextPage": nextPage,
	})
}

// entryFromPath loads the signed in user's entry; other users' entries are "not found".
func (s *Server) entryFromPath(w http.ResponseWriter, r *http.Request) (*Entry, bool, bool) {
	raw := r.PathValue("id")
	isJSON := strings.HasSuffix(raw, ".json")
	id, err := strconv.ParseInt(strings.TrimSuffix(raw, ".json"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, false, false
	}
	e, err := FindEntry(r.Context(), s.db, currentUser(r).ID, id)
	if err != nil {
		s.serverError(w, err)
		return nil, false, false
	}
	if e == nil {
		http.NotFound(w, r)
		return nil, false, false
	}
	return e, isJSON, true
}

func (s *Server) showEntry(w http.ResponseWriter, r *http.Request) {
	e, isJSON, ok := s.entryFromPath(w, r)
	if !ok {
		return
	}
	if isJSON {
		writeJSON(w, asJSON(r, e))
		return
	}
	s.render(w, r, "current_accounts_show", http.StatusOK, map[string]any{"Entry": e})
}

func formData(e *Entry, errs []string) map[string]any {
	return map[string]any{"Entry": e, "Errors": errs, "Costs": Costs, "CashAccounts": CashAccounts}
}

func (s *Server) newEntry(w http.ResponseWriter, r *http.Request) {
	e := &Entry{Date: s.today(), TypeLaunche: Debit, CashAccountID: Unset, CostID: Unset}
	s.render(w, r, "current_accounts_new", http.StatusOK, formData(e, nil))
}

func (s *Server) editEntry(w http.ResponseWriter, r *http.Request) {
	e, _, ok := s.entryFromPath(w, r)
	if !ok {
		return
	}
	s.render(w, r, "current_accounts_edit", http.StatusOK, formData(e, nil))
}

var integerRegexp = regexp.MustCompile(`^-?\d+$`)

// assign copies the form fields into the entry and returns the validation messages.
func assign(e *Entry, r *http.Request) []string {
	integer := func(name string) int {
		value := strings.TrimSpace(r.PostFormValue("current_account[" + name + "]"))
		if !integerRegexp.MatchString(value) {
			return Unset
		}
		n, _ := strconv.Atoi(value)
		return n
	}
	e.CashAccountID = integer("cash_account_id")
	e.CostID = integer("cost_id")
	switch r.PostFormValue("current_account[type_launche]") {
	case "debito":
		e.TypeLaunche = Debit
	case "credito":
		e.TypeLaunche = Credit
	default:
		e.TypeLaunche = 0
	}
	e.Date = time.Time{}
	if day := parseDate(r.PostFormValue("current_account[date_ocurrence]")); day != nil {
		e.Date = *day
	}
	e.PriceText = strings.TrimSpace(r.PostFormValue("current_account[price]"))
	price, err := strconv.ParseFloat(e.PriceText, 64)
	e.PriceSet = err == nil
	if e.PriceSet {
		e.PriceCents = int64(price*100 + 0.5*sign(price))
	}
	e.Historic = nil
	if historic := r.PostFormValue("current_account[historic]"); historic != "" {
		e.Historic = &historic
	}
	return e.Validate()
}

func sign(value float64) float64 {
	if value < 0 {
		return -1
	}
	return 1
}

func (s *Server) createEntry(w http.ResponseWriter, r *http.Request) {
	e := &Entry{UserID: currentUser(r).ID}
	if errs := assign(e, r); len(errs) > 0 {
		s.render(w, r, "current_accounts_new", http.StatusUnprocessableEntity, formData(e, errs))
		return
	}
	if err := SaveEntry(r.Context(), s.db, e); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirectWith(w, r, "/current_accounts", http.StatusFound, flash{Notice: "Lançamento cadastrado."})
}

func (s *Server) updateEntry(w http.ResponseWriter, r *http.Request) {
	e, _, ok := s.entryFromPath(w, r)
	if !ok {
		return
	}
	if errs := assign(e, r); len(errs) > 0 {
		s.render(w, r, "current_accounts_edit", http.StatusUnprocessableEntity, formData(e, errs))
		return
	}
	if err := SaveEntry(r.Context(), s.db, e); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirectWith(w, r, "/current_accounts/"+strconv.FormatInt(e.ID, 10), http.StatusFound, flash{Notice: "Lançamento atualizado."})
}

func (s *Server) destroyEntry(w http.ResponseWriter, r *http.Request) {
	e, _, ok := s.entryFromPath(w, r)
	if !ok {
		return
	}
	if err := DeleteEntry(r.Context(), s.db, e); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirectWith(w, r, "/current_accounts", http.StatusSeeOther, flash{Notice: "Lançamento removido."})
}

// Dashboard

// amountsTable is the data of one dashboard table (the "amounts" partial).
func amountsTable(header string, rows []Row, totalLabel string, total int64) map[string]any {
	return map[string]any{"Header": header, "Rows": rows, "TotalLabel": totalLabel, "Total": total}
}

var monthRegexp = regexp.MustCompile(`^(\d{4})-(\d{1,2})$`)

func (s *Server) parseMonth(value string) time.Time {
	if match := monthRegexp.FindStringSubmatch(value); match != nil {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		if month >= 1 && month <= 12 {
			return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	today := s.today()
	return time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (s *Server) showDashboard(w http.ResponseWriter, r *http.Request) {
	month := s.parseMonth(r.URL.Query().Get("month"))
	summary, err := SummarizeMonth(r.Context(), s.db, currentUser(r).ID, month)
	if err != nil {
		s.serverError(w, err)
		return
	}
	label := months[month.Month()-1] + " de " + strconv.Itoa(month.Year())
	label = strings.ToUpper(label[:1]) + label[1:]
	s.render(w, r, "dashboards_show", http.StatusOK, map[string]any{
		"Nav": "dashboard", "MonthLabel": label, "Today": s.today().Format("2006-01-02"),
		"PreviousMonth": month.AddDate(0, -1, 0).Format("2006-01"), "NextMonth": month.AddDate(0, 1, 0).Format("2006-01"),
		"Summary": summary,
	})
}
