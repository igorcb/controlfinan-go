package app

import (
	"strconv"
	"strings"
)

// Money is kept in cents (int64) to avoid floating point errors.

// Currency formats like Rails number_to_currency in pt-BR: "R$ 1.234,50"; negatives as "-R$ 1.234,50".
func Currency(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	units := strconv.FormatInt(cents/100, 10)
	var grouped strings.Builder
	for i, digit := range units {
		if i > 0 && (len(units)-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(digit)
	}
	return sign + "R$ " + grouped.String() + "," + pad2(cents%100)
}

// AmountClass is the color of an amount: red when negative, green otherwise.
func AmountClass(cents int64) string {
	if cents < 0 {
		return "text-red-700"
	}
	return "text-emerald-700"
}

// DecimalText prints cents like Ruby's BigDecimal#to_s: 15000 -> "150.0", 1250 -> "12.5", 5 -> "0.05".
func DecimalText(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	fraction := strings.TrimRight(pad2(cents%100), "0")
	if fraction == "" {
		fraction = "0"
	}
	return sign + strconv.FormatInt(cents/100, 10) + "." + fraction
}

// ParseCents reads a Postgres numeric as text ("-12.5", "150.00", "0") into cents.
func ParseCents(text string) int64 {
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	units, fraction, _ := strings.Cut(text, ".")
	fraction = (fraction + "00")[:2]
	value, _ := strconv.ParseInt(units, 10, 64)
	cents, _ := strconv.ParseInt(fraction, 10, 64)
	total := value*100 + cents
	if negative {
		return -total
	}
	return total
}

func pad2(value int64) string {
	if value < 10 {
		return "0" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}
