// Package money handles amounts of money. An amount is an int64 count of its currency's minor unit (cents for USD), never a
// float.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Currency is an ISO 4217 currency code.
type Currency string

// The currencies Tally bills in.
const (
	USD Currency = "USD"
	EUR Currency = "EUR"
	GBP Currency = "GBP"
	JPY Currency = "JPY"
	KWD Currency = "KWD"
)

var symbols = map[Currency]string{USD: "$", EUR: "€", GBP: "£", JPY: "¥", KWD: "KWD "}

// Valid reports whether c is a currency Tally bills in.
func (c Currency) Valid() bool {
	_, ok := symbols[c]
	return ok
}

// ErrInvalidAmount is returned for an amount that cannot be read.
var ErrInvalidAmount = errors.New("invalid amount")

// ParseMajor reads an amount a person typed in major units ("12.50", "7") and returns it in minor units.
func ParseMajor(s string, c Currency) (int64, error) {
	if !c.Valid() {
		return 0, fmt.Errorf("%w: unknown currency %q", ErrInvalidAmount, c)
	}
	s = strings.TrimSpace(s)
	whole, frac, hasDot := strings.Cut(s, ".")
	if whole == "" || (hasDot && frac == "") || len(frac) > 2 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
		}
	}
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	return n, nil
}

// Format renders an amount for people: "$1,234.50", "-€0.99".
func Format(amount int64, c Currency) string {
	sign := ""
	if amount < 0 {
		sign, amount = "-", -amount
	}
	whole, frac := amount/100, amount%100
	return fmt.Sprintf("%s%s%s.%02d", sign, symbols[c], group(whole), frac)
}

// group writes n with a comma between each group of three digits.
func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Percent returns pct percent of amount, rounded half away from zero to a whole minor unit.
func Percent(amount int64, pct int) int64 { return divRound(amount*int64(pct), 100) }

// BasisPoints returns bp basis points (hundredths of a percent) of amount, rounded half away from zero.
func BasisPoints(amount int64, bp int) int64 { return divRound(amount*int64(bp), 10000) }

// divRound divides and rounds half away from zero.
func divRound(num, den int64) int64 {
	if (num < 0) != (den < 0) {
		return -((abs(num) + abs(den)/2) / abs(den))
	}
	return (abs(num) + abs(den)/2) / abs(den)
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
