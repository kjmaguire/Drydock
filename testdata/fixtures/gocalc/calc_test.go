package gocalc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2,3) = %d, want 5", got)
	}
}

func TestSubtract(t *testing.T) {
	if got := Subtract(5, 3); got != 2 {
		t.Fatalf("Subtract(5,3) = %d, want 2", got)
	}
}

func TestSubtractNegative(t *testing.T) {
	if got := Subtract(3, 5); got != -2 {
		t.Fatalf("Subtract(3,5) = %d, want -2", got)
	}
}
