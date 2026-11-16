package api

import (
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestLoginAcceptsSupportedPasswordLengths(t *testing.T) {
	validate := validator.New()
	for _, size := range []int{8, 32, 48, 72} {
		form := LoginForm{Username: "user", Password: strings.Repeat("a", size)}
		if err := validate.Struct(form); err != nil {
			t.Fatalf("password length %d: %v", size, err)
		}
	}
	for _, size := range []int{0, 73} {
		form := LoginForm{Username: "user", Password: strings.Repeat("a", size)}
		if err := validate.Struct(form); err == nil {
			t.Fatalf("accepted password length %d", size)
		}
	}
}
