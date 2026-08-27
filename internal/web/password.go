package web

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is deliberately above the library default: a login is a rare
// operation, so ~250ms per attempt is a good trade against offline cracking.
const bcryptCost = 12

// HashPassword returns a bcrypt hash suitable for auth.password_hash.
func HashPassword(password string) (string, error) {
	return hashPassword(password, bcryptCost)
}

// hashPassword takes an explicit cost so tests can use bcrypt.MinCost; at the
// production cost every hash takes ~250ms, which makes a test suite unusable.
func hashPassword(password string, cost int) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// checkPassword reports whether password matches the bcrypt hash. A malformed
// hash is a mismatch, never a crash.
func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
