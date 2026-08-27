package web

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestCheckPassword(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple", bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	tests := []struct {
		name     string
		hash     string
		password string
		want     bool
	}{
		{"correct password", hash, "correct horse battery staple", true},
		{"wrong password", hash, "hunter2", false},
		{"empty password", hash, "", false},
		{"malformed hash", "not-a-hash", "correct horse battery staple", false},
		{"empty hash", "", "correct horse battery staple", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkPassword(tt.hash, tt.password); got != tt.want {
				t.Errorf("checkPassword() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHashPasswordUsesProductionCost(t *testing.T) {
	hash, err := HashPassword("whatever")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2a$") {
		t.Errorf("hash must be bcrypt, got %q", hash)
	}

	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if cost != bcryptCost {
		t.Errorf("hash cost = %d, want %d", cost, bcryptCost)
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("an empty password must be rejected, not hashed")
	}
}
