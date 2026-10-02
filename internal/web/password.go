package web

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Above the default: ~250ms per login is a good trade against offline cracking.
const bcryptCost = 12

func HashPassword(password string) (string, error) {
	return hashPassword(password, bcryptCost)
}

// The cost is a parameter so tests can use bcrypt.MinCost.
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

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
