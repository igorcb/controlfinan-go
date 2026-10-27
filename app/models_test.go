package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestUserPasswordUsesTheRailsBcryptFormat(t *testing.T) {
	newTestApp(t)
	u := &User{}
	if err := u.SetPassword("secret123", 4); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.PasswordDigest, "$2a$") {
		t.Errorf("digest %q should start with $2a$", u.PasswordDigest)
	}
	assertEqual(t, u.CheckPassword("secret123"), true)
	assertEqual(t, u.CheckPassword("wrong"), false)
}

func TestUserAcceptsADigestWrittenByRails(t *testing.T) {
	// Digest of "benchmark123" written by the Rails app (has_secure_password, bcrypt-ruby) for its benchmark account.
	u := &User{PasswordDigest: "$2a$12$xUBQqXOC4yO3ZUCU3U./FeJ/CU4pqmbNxFQ5iUUrWzLWmKSpUKChe"}
	assertEqual(t, u.CheckPassword("benchmark123"), true)
	assertEqual(t, u.CheckPassword("wrong"), false)
}

func TestValidateUserReturnsTheRailsMessages(t *testing.T) {
	ta := newTestApp(t)
	ta.createUser("taken@example.com", "secret123")
	ctx := context.Background()
	blank, short, other := "", "123", "456"

	errs := ValidateUser(ctx, testDB, &User{}, &blank, &blank, true)
	assertEqual(t, strings.Join(errs, "|"), "Senha não pode ficar em branco|E-mail não pode ficar em branco")

	errs = ValidateUser(ctx, testDB, &User{EmailAddress: "bad"}, &short, &other, true)
	assertEqual(t, strings.Join(errs, "|"), "Confirmação da senha não é igual a Senha|E-mail não é válido|Senha é muito curto (mínimo: 6 caracteres)")

	good := "secret123"
	errs = ValidateUser(ctx, testDB, &User{EmailAddress: " TAKEN@example.com "}, &good, &good, true)
	assertEqual(t, strings.Join(errs, "|"), "E-mail já está em uso")
}

func TestEntryValidateReturnsTheRailsMessages(t *testing.T) {
	e := &Entry{CashAccountID: Unset, CostID: Unset}
	assertEqual(t, strings.Join(e.Validate(), "|"),
		"Tipo não está incluído na lista|Conta não está incluído na lista|Categoria não está incluído na lista|"+
			"Data não pode ficar em branco|Valor não pode ficar em branco|Valor não é um número")

	e = &Entry{CashAccountID: 0, CostID: 0, TypeLaunche: Debit, Date: time.Now(), PriceSet: true, PriceCents: 0}
	assertEqual(t, strings.Join(e.Validate(), "|"), "Valor deve ser maior que 0")

	e = &Entry{CashAccountID: 99, CostID: 99, TypeLaunche: Credit, Date: time.Now(), PriceText: "abc"}
	assertEqual(t, strings.Join(e.Validate(), "|"), "Conta não está incluído na lista|Categoria não está incluído na lista|Valor não é um número")
}

func TestEntryNamesWithFallback(t *testing.T) {
	e := &Entry{CashAccountID: 1, CostID: 99, TypeLaunche: Debit, PriceCents: 1000}
	assertEqual(t, e.NameAccount(), "Itaú")
	assertEqual(t, e.NameCost(), "Não definido")
	assertEqual(t, e.TypeName(), "Débito")
	assertEqual(t, e.SignedCents(), int64(-1000))
}

func TestBalanceIsCreditsMinusDebits(t *testing.T) {
	ta := newTestApp(t)
	u := ta.createUser("driver@example.com", "secret123")
	ta.createEntry(u, entryAttrs{cents: 15000})
	ta.createEntry(u, entryAttrs{kind: Debit, cents: 4050})

	balance, err := Balance(context.Background(), testDB, u.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, balance, int64(10950))
}

func TestSummarizeMonthGroupsByDayAndDiscountsRideExpenses(t *testing.T) {
	ta := newTestApp(t)
	u := ta.createUser("driver@example.com", "secret123")
	ta.createEntry(u, entryAttrs{date: "2026-10-02", cents: 20000})
	ta.createEntry(u, entryAttrs{date: "2026-10-02", kind: Debit, cost: 0, cents: 5000})
	ta.createEntry(u, entryAttrs{date: "2026-10-05", kind: Debit, cost: 8, cents: 2000})
	ta.createEntry(u, entryAttrs{date: "2026-09-30", cents: 99900})

	s, err := SummarizeMonth(context.Background(), testDB, u.ID, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, len(s.Daily), 2)
	assertEqual(t, s.Daily[0], Row{"05/10/2026", -2000})
	assertEqual(t, s.Daily[1], Row{"02/10/2026", 15000})
	assertEqual(t, s.MonthBalance, int64(13000))
	assertEqual(t, len(s.Monthly), 4)
	assertEqual(t, s.Monthly[0], Row{"Corridas", 20000})
	assertEqual(t, s.Monthly[1], Row{"Combustível", -5000})
	assertEqual(t, s.Monthly[2], Row{"Lavagem", -2000})
	assertEqual(t, s.Monthly[3], Row{"Telefone", 0})
	assertEqual(t, s.RidesNet, int64(13000))
}
