package admin

import (
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/lejianwen/rustdesk-api/v2/model"
)

func TestCreateUserRequiresInitialPassword(t *testing.T) {
	validate := validator.New()
	form := CreateUserForm{UserForm: UserForm{Username: "new-user", GroupId: 1, Status: model.COMMON_STATUS_ENABLE}}
	for _, password := range []string{"", "short", strings.Repeat("a", 73)} {
		form.Password = password
		if err := validate.Struct(form); err == nil {
			t.Fatalf("accepted password length %d", len(password))
		}
	}
	for _, size := range []int{8, 48, 72} {
		form.Password = strings.Repeat("a", size)
		if err := validate.Struct(form); err != nil {
			t.Fatalf("password length %d: %v", size, err)
		}
	}
	if err := validate.Struct(form.UserForm); err != nil {
		t.Fatalf("profile updates must not require a password: %v", err)
	}
}
