package utils

import (
	"errors"
	"golang.org/x/crypto/bcrypt"
)

// EncryptPassword accepts 8–72 bytes, bcrypt's supported input range.
func EncryptPassword(password string) (string, error) {
	if len(password) < 8 || len(password) > 72 {
		return "", errors.New("password must contain 8 to 72 bytes")
	}
	bs, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bs), err
}

// VerifyPassword accepts only bcrypt. Legacy hashes require an explicit reset.
func VerifyPassword(hash, input string) (bool, error) {
	if len(input) > 72 {
		return false, nil
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input))
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, nil
	}
	return err == nil, err
}
