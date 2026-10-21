package money_test

// Hidden acceptance test of the trial task tally-currency-minor-units (evals/tasks). The agents never see it.

import (
	"errors"
	"math"
	"testing"

	"example.com/tally/internal/money"
)

func TestHiddenMinorUnits(t *testing.T) {
	for _, c := range []struct {
		cur  money.Currency
		want int
	}{{money.USD, 2}, {money.EUR, 2}, {money.GBP, 2}, {money.JPY, 0}, {money.KWD, 3}} {
		if got := c.cur.MinorUnits(); got != c.want {
			t.Errorf("%s.MinorUnits() = %d, want %d", c.cur, got, c.want)
		}
	}
}

func TestHiddenParseMajor(t *testing.T) {
	for _, c := range []struct {
		in   string
		cur  money.Currency
		want int64
	}{
		// the prompt's examples
		{"12.5", money.USD, 1250},
		{"7", money.KWD, 7000},
		// two decimals
		{"12.50", money.USD, 1250},
		{"7", money.USD, 700},
		{"0.99", money.EUR, 99},
		{"0.9", money.EUR, 90},
		{"12", money.GBP, 1200},
		{"1234.56", money.GBP, 123456},
		// no decimals
		{"1000", money.JPY, 1000},
		{"0", money.JPY, 0},
		{"5", money.JPY, 5},
		{"1234567", money.JPY, 1234567},
		// three decimals
		{"1.250", money.KWD, 1250},
		{"1.25", money.KWD, 1250},
		{"1.2", money.KWD, 1200},
		{"0.005", money.KWD, 5},
		{"0", money.KWD, 0},
		{"1234.567", money.KWD, 1234567},
	} {
		got, err := money.ParseMajor(c.in, c.cur)
		if err != nil || got != c.want {
			t.Errorf("ParseMajor(%q, %s) = %d, %v; want %d, nil", c.in, c.cur, got, err, c.want)
		}
	}
	for _, c := range []struct {
		in  string
		cur money.Currency
	}{
		// more decimals than the currency has
		{"12.345", money.USD}, {"0.001", money.EUR}, {"1.999", money.GBP}, {"1.2345", money.KWD}, {"0.0001", money.KWD},
		// JPY: no decimal point at all
		{"1000.0", money.JPY}, {"1000.", money.JPY}, {"5.5", money.JPY}, {"0.0", money.JPY}, {"1.000", money.JPY},
		// signs
		{"-1", money.USD}, {"+1", money.USD}, {"-5", money.JPY}, {"+5", money.JPY}, {"-0.005", money.KWD}, {"+7", money.KWD}, {"-0", money.KWD},
		// spaces inside the number
		{"1 000", money.USD}, {"12. 50", money.USD}, {"1 000", money.JPY}, {"1 .250", money.KWD}, {"7 000", money.KWD},
		// thousands separators
		{"1,000", money.USD}, {"1,000.00", money.USD}, {"1,000", money.JPY}, {"1,000.000", money.KWD}, {"1,234", money.KWD},
		// empty, or not a number
		{"", money.USD}, {"", money.JPY}, {"", money.KWD}, {"   ", money.KWD}, {"abc", money.JPY}, {"1.2.3", money.KWD},
	} {
		if got, err := money.ParseMajor(c.in, c.cur); !errors.Is(err, money.ErrInvalidAmount) {
			t.Errorf("ParseMajor(%q, %s) = %d, %v; want an error matching ErrInvalidAmount", c.in, c.cur, got, err)
		}
	}
}

func TestHiddenFormat(t *testing.T) {
	for _, c := range []struct {
		amount int64
		cur    money.Currency
		want   string
	}{
		// the prompt's examples
		{123450, money.USD, "$1,234.50"},
		{99, money.EUR, "€0.99"},
		{1200, money.GBP, "£12.00"},
		{1000, money.JPY, "¥1,000"},
		{1250, money.KWD, "KWD 1.250"},
		{-500, money.USD, "-$5.00"},
		{-5, money.JPY, "-¥5"},
		{-5, money.KWD, "-KWD 0.005"},
		// two decimals
		{0, money.USD, "$0.00"},
		{5, money.USD, "$0.05"},
		{-99, money.EUR, "-€0.99"},
		{123456789, money.GBP, "£1,234,567.89"},
		// no decimals
		{0, money.JPY, "¥0"},
		{999, money.JPY, "¥999"},
		{1234567, money.JPY, "¥1,234,567"},
		{-1234567, money.JPY, "-¥1,234,567"},
		{math.MaxInt64, money.JPY, "¥9,223,372,036,854,775,807"},
		// three decimals
		{0, money.KWD, "KWD 0.000"},
		{1, money.KWD, "KWD 0.001"},
		{50, money.KWD, "KWD 0.050"},
		{7000, money.KWD, "KWD 7.000"},
		{1234567, money.KWD, "KWD 1,234.567"},
		{-1234567, money.KWD, "-KWD 1,234.567"},
		{math.MaxInt64, money.KWD, "KWD 9,223,372,036,854,775.807"},
	} {
		if got := money.Format(c.amount, c.cur); got != c.want {
			t.Errorf("Format(%d, %s) = %q, want %q", c.amount, c.cur, got, c.want)
		}
	}
}
