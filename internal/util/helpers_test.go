package util

import "testing"

func TestBoolValue(t *testing.T) {
	if got := BoolValue(nil, true); got != true {
		t.Fatalf("BoolValue(nil, true) = %v, want true", got)
	}
	if got := BoolValue(nil, false); got != false {
		t.Fatalf("BoolValue(nil, false) = %v, want false", got)
	}
	val := true
	if got := BoolValue(&val, false); got != true {
		t.Fatalf("BoolValue(true, false) = %v, want true", got)
	}
	val = false
	if got := BoolValue(&val, true); got != false {
		t.Fatalf("BoolValue(false, true) = %v, want false", got)
	}
}
