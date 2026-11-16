package jwt

import (
	"strings"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

func TestFixedUniqueSessions(t *testing.T) {
	s := NewJwt(strings.Repeat("k", 32), time.Hour)
	a, exp, err := s.GenerateToken(42)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.GenerateToken(42)
	if err != nil || a == b {
		t.Fatal("sessions must have independent IDs")
	}
	uid, err := s.ParseToken(a)
	if err != nil || uid != 42 {
		t.Fatal("valid session rejected", err)
	}
	claims := &UserClaims{}
	_, err = jwtlib.ParseWithClaims(a, claims, func(*jwtlib.Token) (interface{}, error) { return s.Key, nil })
	if err != nil || claims.ExpiresAt.Unix() != exp || claims.ID == "" || claims.IssuedAt == nil {
		t.Fatal("invalid session claims")
	}
}

func TestRejectInvalidConfiguration(t *testing.T) {
	for _, s := range []*Jwt{nil, NewJwt("", time.Hour), NewJwt("short", time.Hour), NewJwt(strings.Repeat("k", 32), 0)} {
		if _, _, err := s.GenerateToken(1); err == nil {
			t.Fatal("accepted invalid signing configuration")
		}
		if _, err := s.ParseToken("garbage"); err == nil {
			t.Fatal("accepted invalid verification configuration")
		}
	}
}

func TestRejectInvalidTokens(t *testing.T) {
	s := NewJwt(strings.Repeat("k", 32), time.Hour)
	now := time.Now().Unix()
	valid := func() jwtlib.MapClaims {
		return jwtlib.MapClaims{"user_id": 1, "exp": now + 3600, "iat": now, "jti": "test"}
	}
	tests := []struct {
		name   string
		method jwtlib.SigningMethod
		claims jwtlib.MapClaims
		key    []byte
	}{
		{"wrong key", jwtlib.SigningMethodHS256, valid(), []byte(strings.Repeat("x", 32))},
		{"wrong algorithm", jwtlib.SigningMethodHS384, valid(), s.Key},
	}
	for _, field := range []string{"exp", "iat", "jti", "user_id"} {
		c := valid()
		delete(c, field)
		tests = append(tests, struct {
			name   string
			method jwtlib.SigningMethod
			claims jwtlib.MapClaims
			key    []byte
		}{"missing " + field, jwtlib.SigningMethodHS256, c, s.Key})
	}
	for _, offset := range []int64{-1, 0} {
		c := valid()
		c["exp"] = now + offset
		tests = append(tests, struct {
			name   string
			method jwtlib.SigningMethod
			claims jwtlib.MapClaims
			key    []byte
		}{"expired", jwtlib.SigningMethodHS256, c, s.Key})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := jwtlib.NewWithClaims(tt.method, tt.claims).SignedString(tt.key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ParseToken(token); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
	for _, token := range []string{"", "garbage", "a.b.c"} {
		if _, err := s.ParseToken(token); err == nil {
			t.Fatal("malformed token accepted")
		}
	}
}
