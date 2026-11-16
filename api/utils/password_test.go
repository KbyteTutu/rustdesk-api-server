package utils

import (
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	hash, err := EncryptPassword("correct password")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword(hash, "correct password"); !ok || err != nil {
		t.Fatal("bcrypt verify failed", err)
	}
	if ok, _ := VerifyPassword(hash, "wrong password"); ok {
		t.Fatal("wrong password accepted")
	}
	if ok, _ := VerifyPassword(Md5("secret"+"rustdesk-api"), "secret"); ok {
		t.Fatal("legacy MD5 accepted")
	}
	for _, p := range []string{"", "short", strings.Repeat("x", 73), strings.Repeat("中", 25)} {
		if _, err := EncryptPassword(p); err == nil {
			t.Fatal("invalid byte length accepted")
		}
	}
	if _, err := EncryptPassword(strings.Repeat("x", 72)); err != nil {
		t.Fatal(err)
	}
}
