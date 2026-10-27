package app

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var CashAccounts = []Option{
	{0, "Conta Dinheiro"}, {1, "Itaú"}, {2, "Santander"}, {3, "CEF Poupança"}, {4, "PagSeguro"},
	{5, "Uber"}, {6, "Platinum Itaú"}, {7, "Banco do Brasil"}, {8, "99Pop"},
}

var Costs = []Option{
	{0, "Combustível"}, {1, "Central"}, {2, "Aluguel Carro"}, {3, "Almoço"}, {4, "Lanche"}, {5, "Diversos"},
	{6, "Corridas"}, {7, "Telefone"}, {8, "Lavagem"}, {9, "Transferência"}, {10, "Depósitos"},
	{11, "Estacionamento"}, {12, "Impostos"}, {13, "Multas"}, {14, "Manutenção"}, {15, "Viagens Uber"},
}

const (
	Debit  = -1
	Credit = 1
	Rides  = 6
	// Unset marks an integer form field that was blank or invalid.
	Unset = -1
)

// RideExpenses are discounted from the rides in the monthly balance (legacy dashboard rule), in this order.
var RideExpenses = []Option{{0, "Combustível"}, {8, "Lavagem"}, {7, "Telefone"}}

// Same regexp as Ruby's URI::MailTo::EMAIL_REGEXP.
var emailRegexp = regexp.MustCompile(`^[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

type Option struct {
	ID   int
	Name string
}

func optionName(options []Option, id int) string {
	for _, option := range options {
		if option.ID == id {
			return option.Name
		}
	}
	return "Não definido"
}

func hasOption(options []Option, id int) bool {
	for _, option := range options {
		if option.ID == id {
			return true
		}
	}
	return false
}

// User mirrors the Rails users table (has_secure_password).
type User struct {
	ID             int64
	EmailAddress   string
	PasswordDigest string
}

func NormalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// SetPassword stores a bcrypt digest; Go writes the same "$2a$" format as Rails.
func (u *User) SetPassword(raw string, cost int) error {
	digest, err := bcrypt.GenerateFromPassword(truncate72(raw), cost)
	if err != nil {
		return err
	}
	u.PasswordDigest = string(digest)
	return nil
}

func (u *User) CheckPassword(raw string) bool {
	return raw != "" && bcrypt.CompareHashAndPassword([]byte(u.PasswordDigest), truncate72(raw)) == nil
}

// bcrypt only uses the first 72 bytes, like Rails.
func truncate72(raw string) []byte {
	bytes := []byte(raw)
	if len(bytes) > 72 {
		bytes = bytes[:72]
	}
	return bytes
}

// ValidateUser returns the Rails messages (pt-BR) in the same order.
func ValidateUser(ctx context.Context, db *pgxpool.Pool, u *User, password, confirmation *string, passwordRequired bool) []string {
	var errs []string
	if passwordRequired && (password == nil || *password == "") {
		errs = append(errs, "Senha não pode ficar em branco")
	}
	if password != nil && confirmation != nil && *password != *confirmation {
		errs = append(errs, "Confirmação da senha não é igual a Senha")
	}
	u.EmailAddress = NormalizeEmail(u.EmailAddress)
	switch {
	case u.EmailAddress == "":
		errs = append(errs, "E-mail não pode ficar em branco")
	case !emailRegexp.MatchString(u.EmailAddress):
		errs = append(errs, "E-mail não é válido")
	default:
		var exists bool
		db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE email_address = $1 AND id <> $2)", u.EmailAddress, u.ID).Scan(&exists)
		if exists {
			errs = append(errs, "E-mail já está em uso")
		}
	}
	if password != nil && *password != "" && utf8.RuneCountInString(*password) < 6 {
		errs = append(errs, "Senha é muito curto (mínimo: 6 caracteres)")
	}
	return errs
}

func FindUserByEmail(ctx context.Context, db *pgxpool.Pool, email string) (*User, error) {
	u := &User{}
	err := db.QueryRow(ctx, "SELECT id, email_address, password_digest FROM users WHERE email_address = $1", NormalizeEmail(email)).
		Scan(&u.ID, &u.EmailAddress, &u.PasswordDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func FindUser(ctx context.Context, db *pgxpool.Pool, id int64) (*User, error) {
	u := &User{}
	err := db.QueryRow(ctx, "SELECT id, email_address, password_digest FROM users WHERE id = $1", id).
		Scan(&u.ID, &u.EmailAddress, &u.PasswordDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func CreateUser(ctx context.Context, db *pgxpool.Pool, u *User) error {
	return db.QueryRow(ctx,
		"INSERT INTO users (email_address, password_digest, created_at, updated_at) VALUES ($1, $2, now(), now()) RETURNING id",
		u.EmailAddress, u.PasswordDigest).Scan(&u.ID)
}

func UpdatePassword(ctx context.Context, db *pgxpool.Pool, u *User) error {
	_, err := db.Exec(ctx, "UPDATE users SET password_digest = $1, updated_at = now() WHERE id = $2", u.PasswordDigest, u.ID)
	return err
}

// Entry is a row of current_accounts (a credit or a debit).
type Entry struct {
	ID            int64
	UserID        int64
	CashAccountID int
	CostID        int
	TypeLaunche   int
	Date          time.Time
	PriceCents    int64
	PriceSet      bool
	PriceText     string // raw form value, used by the validation like Rails numericality
	Historic      *string
}

func (e *Entry) Persisted() bool     { return e.ID != 0 }
func (e *Entry) IsCredit() bool      { return e.TypeLaunche == Credit }
func (e *Entry) NameAccount() string { return optionName(CashAccounts, e.CashAccountID) }
func (e *Entry) NameCost() string    { return optionName(Costs, e.CostID) }
func (e *Entry) DateBR() string      { return e.Date.Format("02/01/2006") }
func (e *Entry) DayMonth() string    { return e.Date.Format("02/01") }

func (e *Entry) DateISO() string {
	if e.Date.IsZero() {
		return ""
	}
	return e.Date.Format("2006-01-02")
}

func (e *Entry) TypeName() string {
	if e.IsCredit() {
		return "Crédito"
	}
	return "Débito"
}

func (e *Entry) SignedCents() int64 {
	if e.IsCredit() {
		return e.PriceCents
	}
	return -e.PriceCents
}

func (e *Entry) HistoricText() string {
	if e.Historic == nil {
		return ""
	}
	return *e.Historic
}

// PriceField is the value shown in the form: the decimal like Rails, or what the user typed.
func (e *Entry) PriceField() string {
	if e.PriceSet {
		return DecimalText(e.PriceCents)
	}
	return e.PriceText
}

// Validate returns the Rails messages (pt-BR) in the same order as the model validations.
func (e *Entry) Validate() []string {
	var errs []string
	if e.TypeLaunche != Debit && e.TypeLaunche != Credit {
		errs = append(errs, "Tipo não está incluído na lista")
	}
	if !hasOption(CashAccounts, e.CashAccountID) {
		errs = append(errs, "Conta não está incluído na lista")
	}
	if !hasOption(Costs, e.CostID) {
		errs = append(errs, "Categoria não está incluído na lista")
	}
	if e.Date.IsZero() {
		errs = append(errs, "Data não pode ficar em branco")
	}
	// Like Rails presence + numericality: a blank value is also "not a number".
	switch {
	case !e.PriceSet && e.PriceText == "":
		errs = append(errs, "Valor não pode ficar em branco", "Valor não é um número")
	case !e.PriceSet:
		errs = append(errs, "Valor não é um número")
	case e.PriceCents <= 0:
		errs = append(errs, "Valor deve ser maior que 0")
	}
	return errs
}

const entryColumns = "id, user_id, cash_account_id, cost_id, type_launche, date_ocurrence, price::text, historic"

func scanEntry(row pgx.Row) (*Entry, error) {
	e := &Entry{PriceSet: true}
	var price string
	if err := row.Scan(&e.ID, &e.UserID, &e.CashAccountID, &e.CostID, &e.TypeLaunche, &e.Date, &price, &e.Historic); err != nil {
		return nil, err
	}
	e.PriceCents = ParseCents(price)
	return e, nil
}

// ListEntries returns up to limit entries, most recent first, optionally on one day.
func ListEntries(ctx context.Context, db *pgxpool.Pool, userID int64, day *time.Time, limit, offset int) ([]*Entry, error) {
	query := "SELECT " + entryColumns + " FROM current_accounts WHERE user_id = $1"
	args := []any{userID}
	if day != nil {
		query += " AND date_ocurrence = $2"
		args = append(args, *day)
	}
	query += " ORDER BY date_ocurrence DESC, id DESC LIMIT " + itoa(limit) + " OFFSET " + itoa(offset)
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Balance is credits minus debits: the sign comes from type_launche (-1 or 1).
func Balance(ctx context.Context, db *pgxpool.Pool, userID int64, day *time.Time) (int64, error) {
	query := "SELECT COALESCE(SUM(price * type_launche), 0)::text FROM current_accounts WHERE user_id = $1"
	args := []any{userID}
	if day != nil {
		query += " AND date_ocurrence = $2"
		args = append(args, *day)
	}
	var total string
	err := db.QueryRow(ctx, query, args...).Scan(&total)
	return ParseCents(total), err
}

func FindEntry(ctx context.Context, db *pgxpool.Pool, userID, id int64) (*Entry, error) {
	e, err := scanEntry(db.QueryRow(ctx, "SELECT "+entryColumns+" FROM current_accounts WHERE user_id = $1 AND id = $2", userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

func SaveEntry(ctx context.Context, db *pgxpool.Pool, e *Entry) error {
	price := DecimalText(e.PriceCents)
	if e.Persisted() {
		_, err := db.Exec(ctx, `UPDATE current_accounts SET cash_account_id = $1, cost_id = $2, type_launche = $3,
			date_ocurrence = $4, price = $5, historic = $6, updated_at = now() WHERE id = $7 AND user_id = $8`,
			e.CashAccountID, e.CostID, e.TypeLaunche, e.Date, price, e.Historic, e.ID, e.UserID)
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO current_accounts
		(user_id, cash_account_id, cost_id, type_launche, date_ocurrence, price, historic, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now()) RETURNING id`,
		e.UserID, e.CashAccountID, e.CostID, e.TypeLaunche, e.Date, price, e.Historic).Scan(&e.ID)
}

func DeleteEntry(ctx context.Context, db *pgxpool.Pool, e *Entry) error {
	_, err := db.Exec(ctx, "DELETE FROM current_accounts WHERE id = $1 AND user_id = $2", e.ID, e.UserID)
	return err
}

// Row is a label and an amount in the dashboard tables.
type Row struct {
	Label string
	Cents int64
}

// MonthSummary holds the dashboard numbers of one month.
type MonthSummary struct {
	Daily        []Row // balance of each day, most recent first
	MonthBalance int64
	Monthly      []Row // rides, then each ride expense as a negative amount
	RidesNet     int64
}

func SummarizeMonth(ctx context.Context, db *pgxpool.Pool, userID int64, month time.Time) (*MonthSummary, error) {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)
	summary := &MonthSummary{}

	rows, err := db.Query(ctx, `SELECT date_ocurrence, SUM(price * type_launche)::text FROM current_accounts
		WHERE user_id = $1 AND date_ocurrence BETWEEN $2 AND $3 GROUP BY date_ocurrence ORDER BY date_ocurrence DESC`,
		userID, first, last)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day time.Time
		var total string
		if err := rows.Scan(&day, &total); err != nil {
			rows.Close()
			return nil, err
		}
		cents := ParseCents(total)
		summary.Daily = append(summary.Daily, Row{day.Format("02/01/2006"), cents})
		summary.MonthBalance += cents
	}
	rows.Close()

	expenses := map[int]int64{}
	rows, err = db.Query(ctx, `SELECT cost_id, SUM(price)::text FROM current_accounts
		WHERE user_id = $1 AND date_ocurrence BETWEEN $2 AND $3 AND cost_id = ANY($4) GROUP BY cost_id`,
		userID, first, last, []int{0, 8, 7})
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var costID int
		var total string
		if err := rows.Scan(&costID, &total); err != nil {
			rows.Close()
			return nil, err
		}
		expenses[costID] = ParseCents(total)
	}
	rows.Close()

	var rides string
	if err := db.QueryRow(ctx, `SELECT COALESCE(SUM(price * type_launche), 0)::text FROM current_accounts
		WHERE user_id = $1 AND date_ocurrence BETWEEN $2 AND $3 AND cost_id = $4`, userID, first, last, Rides).Scan(&rides); err != nil {
		return nil, err
	}
	ridesCents := ParseCents(rides)
	summary.Monthly = []Row{{"Corridas", ridesCents}}
	summary.RidesNet = ridesCents
	for _, expense := range RideExpenses {
		summary.Monthly = append(summary.Monthly, Row{expense.Name, -expenses[expense.ID]})
		summary.RidesNet -= expenses[expense.ID]
	}
	return summary, nil
}
