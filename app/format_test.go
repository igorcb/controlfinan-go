package app

import "testing"

func TestCurrencyFormatsLikeRailsInPortuguese(t *testing.T) {
	cases := map[int64]string{123450: "R$ 1.234,50", -1250: "-R$ 12,50", 0: "R$ 0,00", 5: "R$ 0,05", 123456789: "R$ 1.234.567,89"}
	for cents, want := range cases {
		assertEqual(t, Currency(cents), want)
	}
}

func TestDecimalTextPrintsLikeRubyBigDecimal(t *testing.T) {
	cases := map[int64]string{15000: "150.0", 1250: "12.5", 5: "0.05", 1234: "12.34", -1250: "-12.5"}
	for cents, want := range cases {
		assertEqual(t, DecimalText(cents), want)
	}
}

func TestParseCentsReadsPostgresNumericText(t *testing.T) {
	cases := map[string]int64{"150.00": 15000, "-12.5": -1250, "0": 0, "7.05": 705}
	for text, want := range cases {
		assertEqual(t, ParseCents(text), want)
	}
}
