package models

import (
	"strings"
	"testing"
)

func TestGenerateID_Format(t *testing.T) {
	allowed := "0123456789abcdefghijklmnopqrstuvwxyz"
	for i := 0; i < 200; i++ {
		id := GenerateID()
		if len(id) != 12 {
			t.Fatalf("expected ID length 12, got %d (%s)", len(id), id)
		}
		for _, ch := range id {
			if !strings.ContainsRune(allowed, ch) {
				t.Fatalf("unexpected character %q in ID %s", ch, id)
			}
		}
	}
}
