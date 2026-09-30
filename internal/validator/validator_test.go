package validator

import "testing"

func TestUUIDRX(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"valid lowercase", "123e4567-e89b-12d3-a456-426614174000", true},
		{"valid uppercase", "123E4567-E89B-12D3-A456-426614174000", true},
		{"empty", "", false},
		{"not a uuid", "abc", false},
		{"trailing character", "123e4567-e89b-12d3-a456-426614174000x", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.value, UUIDRX); got != tt.want {
				t.Errorf("Matches(%q, UUIDRX) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
