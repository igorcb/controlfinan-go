package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLoginFormIsShown(t *testing.T) {
	ta := newTestApp(t)
	res := ta.get("/session/new")
	assertEqual(t, res.status, http.StatusOK)
	assertContains(t, res.body, "Esqueceu a senha?")
}

func TestGuestIsRedirectedToLoginAndBackAfterSigningIn(t *testing.T) {
	ta := newTestApp(t)
	ta.createUser("driver@example.com", "secret123")

	assertEqual(t, ta.get("/dashboard").location, "/session/new")
	res := ta.post("/session", url.Values{"email_address": {" Driver@Example.com "}, "password": {"secret123"}})

	assertEqual(t, res.location, "/dashboard")
	assertEqual(t, count(t, "SELECT count(*) FROM sessions"), 1)
	assertEqual(t, ta.get("/dashboard").status, http.StatusOK)
}

func TestWrongPasswordIsRejected(t *testing.T) {
	ta := newTestApp(t)
	ta.createUser("driver@example.com", "secret123")

	res := ta.post("/session", url.Values{"email_address": {"driver@example.com"}, "password": {"wrong"}})

	assertEqual(t, res.location, "/session/new")
	assertContains(t, ta.get("/session/new").body, "E-mail ou senha inválidos.")
}

func TestLoginAttemptsAreLimitedToTenEveryThreeMinutes(t *testing.T) {
	ta := newTestApp(t)
	form := url.Values{"email_address": {"x@example.com"}, "password": {"x"}}
	for range 10 {
		ta.post("/session", form)
	}
	ta.post("/session", form)
	assertContains(t, ta.get("/session/new").body, "Tente novamente mais tarde.")

	ta.clock.now = ta.clock.now.Add(4 * time.Minute)
	ta.post("/session", form)
	assertContains(t, ta.get("/session/new").body, "E-mail ou senha inválidos.")
}

func TestSignOutDeletesTheSession(t *testing.T) {
	ta := newTestApp(t)
	ta.signIn()

	res := ta.post("/session", url.Values{"_method": {"delete"}})

	assertEqual(t, res.status, http.StatusSeeOther)
	assertEqual(t, res.location, "/session/new")
	assertEqual(t, count(t, "SELECT count(*) FROM sessions"), 0)
	assertEqual(t, ta.get("/").location, "/session/new")
}

func TestFormsWithoutAValidCSRFTokenAreRejected(t *testing.T) {
	ta := newTestApp(t)
	ta.createUser("driver@example.com", "secret123")

	res := ta.post("/session", url.Values{"authenticity_token": {"forged"}, "email_address": {"driver@example.com"}, "password": {"secret123"}})

	assertEqual(t, res.status, http.StatusUnprocessableEntity)
}

func TestRegistrationCreatesAnAccountAndSignsIn(t *testing.T) {
	ta := newTestApp(t)

	res := ta.post("/registration", url.Values{"user[email_address]": {" New@Example.com "}, "user[password]": {"secret123"}, "user[password_confirmation]": {"secret123"}})

	assertEqual(t, res.location, "/")
	assertEqual(t, count(t, "SELECT count(*) FROM users WHERE email_address = 'new@example.com'"), 1)
	assertContains(t, ta.get("/").body, "Conta criada.")
}

func TestRegistrationShowsTheValidationMessages(t *testing.T) {
	ta := newTestApp(t)

	res := ta.post("/registration", url.Values{"user[email_address]": {"bad"}, "user[password]": {"1"}, "user[password_confirmation]": {"2"}})

	assertEqual(t, res.status, http.StatusUnprocessableEntity)
	assertContains(t, res.body, "Confirmação da senha não é igual a Senha", "E-mail não é válido")
}

func TestPasswordResetEmailDoesNotRevealWhetherTheAddressExists(t *testing.T) {
	ta := newTestApp(t)
	ta.createUser("driver@example.com", "secret123")
	notice := "Se o e-mail estiver cadastrado, enviamos as instruções para redefinir a senha."

	assertEqual(t, ta.post("/passwords", url.Values{"email_address": {"driver@example.com"}}).location, "/session/new")
	assertContains(t, ta.get("/session/new").body, notice)
	ta.post("/passwords", url.Values{"email_address": {"missing@example.com"}})
	assertContains(t, ta.get("/session/new").body, notice)

	assertEqual(t, len(ta.mailer.sent), 1)
	assertContains(t, ta.mailer.sent[0], "driver@example.com|Redefinição de senha|", "/passwords/", "/edit", "15 minutos")
}

func TestPasswordResetChangesThePasswordAndEndsEverySession(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	token := ta.app.passwordResetToken(u)

	assertContains(t, ta.get("/passwords/"+token+"/edit").body, "Defina sua nova senha")
	res := ta.post("/passwords/"+token, url.Values{"_method": {"put"}, "password": {"new-secret"}, "password_confirmation": {"new-secret"}})

	assertEqual(t, res.location, "/session/new")
	assertContains(t, ta.get("/session/new").body, "Senha redefinida.")
	assertEqual(t, count(t, "SELECT count(*) FROM sessions"), 0)
	reloaded, _ := FindUser(t.Context(), testDB, u.ID)
	assertEqual(t, reloaded.CheckPassword("new-secret"), true)
}

func TestPasswordResetRejectsPasswordsThatDoNotMatch(t *testing.T) {
	ta := newTestApp(t)
	token := ta.app.passwordResetToken(ta.createUser("driver@example.com", "secret123"))

	res := ta.post("/passwords/"+token, url.Values{"_method": {"put"}, "password": {"new-secret"}, "password_confirmation": {"other"}})

	assertEqual(t, res.location, "/passwords/"+token+"/edit")
	assertContains(t, ta.get("/passwords/"+token+"/edit").body, "As senhas não conferem ou têm menos de 6 caracteres.")
}

func TestPasswordResetTokenExpiresAfterFifteenMinutesAndAfterAPasswordChange(t *testing.T) {
	ta := newTestApp(t)
	u := ta.createUser("driver@example.com", "secret123")
	token := ta.app.passwordResetToken(u)

	ta.clock.now = ta.clock.now.Add(16 * time.Minute)
	assertEqual(t, ta.get("/passwords/"+token+"/edit").location, "/passwords/new")
	assertContains(t, ta.get("/passwords/new").body, "O link de redefinição é inválido ou expirou.")

	ta.clock.now = ta.clock.now.Add(-16 * time.Minute)
	u.SetPassword("another123", 4)
	UpdatePassword(t.Context(), testDB, u)
	assertEqual(t, ta.get("/passwords/"+token+"/edit").location, "/passwords/new")
	assertEqual(t, ta.get("/passwords/invalid/edit").location, "/passwords/new")
}

func TestListShowsEntriesAndTheTotalBalance(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	ta.createEntry(u, entryAttrs{cents: 15000})
	ta.createEntry(u, entryAttrs{kind: Debit, cents: 4000})

	assertContains(t, ta.get("/").body, "Saldo total", "R$ 110,00", "-R$ 40,00")
}

func TestListFiltersByDay(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	ta.createEntry(u, entryAttrs{date: "2026-10-26", cents: 1000})
	ta.createEntry(u, entryAttrs{date: "2026-10-27", cents: 2000})

	body := ta.get("/current_accounts?date=2026-10-26").body
	assertContains(t, body, "Saldo do dia 26/10/2026", "R$ 10,00")
	assertNotContains(t, body, "R$ 20,00")
}

func TestListPaginatesThirtyEntriesPerPage(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	for n := 1; n <= 31; n++ {
		ta.createEntry(u, entryAttrs{historic: fmt.Sprintf("entry %d.", n)})
	}

	first := ta.get("/current_accounts").body
	assertContains(t, first, "Mais antigos →")
	assertNotContains(t, first, "← Mais recentes", "entry 1.")
	second := ta.get("/current_accounts?page=2").body
	assertContains(t, second, "← Mais recentes", "entry 1.")
	assertNotContains(t, second, "Mais antigos →")
}

func TestJSONHasTheJbuilderFields(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	e := ta.createEntry(u, entryAttrs{historic: "Café/Almoço"})

	want := fmt.Sprintf(`[{"id":%d,"cash_account_id":5,"cost_id":6,"date_ocurrence":"2026-10-27","price":"150.0","historic":"Café/Almoço","user_id":%d,"type_launche":1,"url":"%s/current_accounts/%d.json"}]`,
		e.ID, u.ID, ta.server.URL, e.ID)
	assertEqual(t, ta.get("/current_accounts.json").body, want)
	assertEqual(t, ta.get(fmt.Sprintf("/current_accounts/%d.json", e.ID)).body, strings.Trim(want, "[]"))
}

func TestOtherUsersEntriesAreNotFound(t *testing.T) {
	ta := newTestApp(t)
	ta.signIn()
	other := ta.createEntry(ta.createUser("other@example.com", "secret123"), entryAttrs{})
	path := fmt.Sprintf("/current_accounts/%d", other.ID)

	assertEqual(t, ta.get(path).status, http.StatusNotFound)
	assertEqual(t, ta.get(path+"/edit").status, http.StatusNotFound)
	assertEqual(t, ta.post(path, url.Values{"_method": {"delete"}}).status, http.StatusNotFound)
	assertEqual(t, count(t, "SELECT count(*) FROM current_accounts"), 1)
}

func validEntryForm() url.Values {
	return url.Values{
		"current_account[type_launche]": {"credito"}, "current_account[price]": {"150"}, "current_account[date_ocurrence]": {"2026-10-27"},
		"current_account[cost_id]": {"6"}, "current_account[cash_account_id]": {"5"}, "current_account[historic]": {"Corridas do dia"},
	}
}

func TestCreateEntry(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()

	res := ta.post("/current_accounts", validEntryForm())

	assertEqual(t, res.location, "/current_accounts")
	assertContains(t, ta.get("/current_accounts").body, "Lançamento cadastrado.")
	assertEqual(t, count(t, "SELECT count(*) FROM current_accounts WHERE user_id = $1 AND price = 150 AND type_launche = 1", u.ID), 1)
}

func TestCreateEntryIgnoresAUserIDSentInTheForm(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	other := ta.createUser("other@example.com", "secret123")
	form := validEntryForm()
	form.Set("current_account[user_id]", fmt.Sprint(other.ID))

	ta.post("/current_accounts", form)

	assertEqual(t, count(t, "SELECT count(*) FROM current_accounts WHERE user_id = $1", u.ID), 1)
}

func TestCreateEntryShowsTheValidationMessages(t *testing.T) {
	ta := newTestApp(t)
	ta.signIn()

	res := ta.post("/current_accounts", url.Values{"current_account[price]": {""}})

	assertEqual(t, res.status, http.StatusUnprocessableEntity)
	assertContains(t, res.body, "Valor não pode ficar em branco", "Tipo não está incluído na lista")
}

func TestUpdateEntry(t *testing.T) {
	ta := newTestApp(t)
	e := ta.createEntry(ta.signIn(), entryAttrs{})
	form := validEntryForm()
	form.Set("_method", "patch")
	form.Set("current_account[price]", "12.5")

	res := ta.post(fmt.Sprintf("/current_accounts/%d", e.ID), form)

	assertEqual(t, res.location, fmt.Sprintf("/current_accounts/%d", e.ID))
	assertContains(t, ta.get(res.location).body, "Lançamento atualizado.", "R$ 12,50")
}

func TestUpdateEntryKeepsFieldsAbsentFromTheForm(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	e := ta.createEntry(u, entryAttrs{historic: "Corrida"})
	form := url.Values{"_method": {"patch"}, "current_account[price]": {"42"}}

	res := ta.post(fmt.Sprintf("/current_accounts/%d", e.ID), form)

	assertEqual(t, res.location, fmt.Sprintf("/current_accounts/%d", e.ID))
	stored, err := FindEntry(context.Background(), testDB, u.ID, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, stored.PriceCents, int64(4200))
	assertEqual(t, stored.CostID, e.CostID)
	assertEqual(t, stored.CashAccountID, e.CashAccountID)
	assertEqual(t, stored.TypeLaunche, e.TypeLaunche)
	assertEqual(t, stored.Date.Format("2006-01-02"), e.Date.Format("2006-01-02"))
	if stored.Historic == nil || *stored.Historic != "Corrida" {
		t.Fatalf("historic = %v, want Corrida", stored.Historic)
	}
}

func TestUpdateEntryWithoutHistoricAndCostKeepsThem(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	e := ta.createEntry(u, entryAttrs{historic: "Corrida"})
	form := validEntryForm()
	form.Set("_method", "patch")
	form.Del("current_account[historic]")
	form.Del("current_account[cost_id]")

	ta.post(fmt.Sprintf("/current_accounts/%d", e.ID), form)

	stored, err := FindEntry(context.Background(), testDB, u.ID, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, stored.CostID, e.CostID)
	if stored.Historic == nil || *stored.Historic != "Corrida" {
		t.Fatalf("historic = %v, want Corrida", stored.Historic)
	}
}

func TestUpdateEntryWithAnEmptyPresentRequiredFieldFails(t *testing.T) {
	ta := newTestApp(t)
	e := ta.createEntry(ta.signIn(), entryAttrs{})
	form := url.Values{"_method": {"patch"}, "current_account[price]": {""}}

	res := ta.post(fmt.Sprintf("/current_accounts/%d", e.ID), form)

	assertEqual(t, res.status, http.StatusUnprocessableEntity)
	assertContains(t, res.body, "Valor não pode ficar em branco")
}

func TestDeleteEntry(t *testing.T) {
	ta := newTestApp(t)
	e := ta.createEntry(ta.signIn(), entryAttrs{})

	res := ta.post(fmt.Sprintf("/current_accounts/%d", e.ID), url.Values{"_method": {"delete"}})

	assertEqual(t, res.status, http.StatusSeeOther)
	assertContains(t, ta.get("/current_accounts").body, "Lançamento removido.")
	assertEqual(t, count(t, "SELECT count(*) FROM current_accounts"), 0)
}

func TestLegacyDailySearchRedirectsToTheFilter(t *testing.T) {
	ta := newTestApp(t)
	res := ta.get("/search_account?date_calendar_now=2026-10-27")
	assertEqual(t, res.status, http.StatusMovedPermanently)
	assertEqual(t, res.location, "/current_accounts?date=2026-10-27")
}

func TestDashboardShowsTheDailyAndMonthlyBalances(t *testing.T) {
	ta := newTestApp(t)
	u := ta.signIn()
	ta.createEntry(u, entryAttrs{date: "2026-10-02", cents: 20000})
	ta.createEntry(u, entryAttrs{date: "2026-10-02", kind: Debit, cost: 0, cents: 5000})
	ta.createEntry(u, entryAttrs{date: "2026-10-05", kind: Debit, cost: 8, cents: 2000})
	ta.createEntry(u, entryAttrs{date: "2026-09-30", cents: 99900})

	body := ta.get("/dashboard?month=2026-10").body

	assertContains(t, body, "Outubro de 2026", "Saldo Diário", "Saldo Mensal")
	assertInOrder(t, body, "05/10/2026", "-R$ 20,00", "02/10/2026", "R$ 150,00", "Total do mês", "R$ 130,00")
	assertInOrder(t, body, "Corridas", "R$ 200,00", "Combustível", "-R$ 50,00", "Lavagem", "-R$ 20,00", "Telefone", "R$ 0,00", "Saldo do mês", "R$ 130,00")
	assertNotContains(t, body, "999")
}

func TestDashboardLinksToThePreviousAndNextMonths(t *testing.T) {
	ta := newTestApp(t)
	ta.signIn()
	assertContains(t, ta.get("/dashboard?month=2026-01").body, "/dashboard?month=2025-12", "/dashboard?month=2026-02")
}

func TestDashboardFallsBackToTheCurrentMonth(t *testing.T) {
	ta := newTestApp(t)
	ta.signIn()
	ta.clock.now = time.Date(2026, 3, 15, 12, 0, 0, 0, ta.app.location)
	assertContains(t, ta.get("/dashboard?month=bad").body, "Março de 2026")
}
