package money

import (
	"errors"
	"testing"
)

func TestParseMajor(t *testing.T) {
	for _, c := range []struct {
		in   string
		cur  Currency
		want int64
	}{
		{"12.50", USD, 1250},
		{"12.5", USD, 1250},
		{"7", EUR, 700},
		{"0.99", GBP, 99},
		{" 1000.00 ", USD, 100000},
	} {
		got, err := ParseMajor(c.in, c.cur)
		if err != nil || got != c.want {
			t.Errorf("ParseMajor(%q, %s) = %d, %v; want %d", c.in, c.cur, got, err, c.want)
		}
	}
	for _, in := range []string{"", "abc", "-1", "1.", ".5", "1.234", "1,000"} {
		if _, err := ParseMajor(in, USD); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("ParseMajor(%q) = %v, want ErrInvalidAmount", in, err)
		}
	}
	if _, err := ParseMajor("1", "XYZ"); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("unknown currency: %v", err)
	}
}

func TestFormat(t *testing.T) {
	for _, c := range []struct {
		amount int64
		cur    Currency
		want   string
	}{
		{1250, USD, "$12.50"},
		{123456789, USD, "$1,234,567.89"},
		{-99, EUR, "-€0.99"},
		{0, GBP, "£0.00"},
		{100000, GBP, "£1,000.00"},
	} {
		if got := Format(c.amount, c.cur); got != c.want {
			t.Errorf("Format(%d, %s) = %q, want %q", c.amount, c.cur, got, c.want)
		}
	}
}

func TestRounding(t *testing.T) {
	for _, c := range []struct {
		amount int64
		pct    int
		want   int64
	}{
		{1000, 15, 150},
		{999, 10, 100}, // 99.9
		{995, 10, 100}, // 99.5 rounds up
		{994, 10, 99},
		{-995, 10, -100}, // half away from zero
	} {
		if got := Percent(c.amount, c.pct); got != c.want {
			t.Errorf("Percent(%d, %d) = %d, want %d", c.amount, c.pct, got, c.want)
		}
	}
	if got := BasisPoints(10000, 825); got != 825 {
		t.Errorf("BasisPoints(10000, 825) = %d", got)
	}
	if got := BasisPoints(1234, 825); got != 102 { // 101.805
		t.Errorf("BasisPoints(1234, 825) = %d", got)
	}
	if got := BasisPoints(200, 2500); got != 50 {
		t.Errorf("BasisPoints(200, 2500) = %d", got)
	}
}
