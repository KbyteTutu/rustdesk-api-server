package jwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Jwt struct {
	Key                 []byte
	TokenExpireDuration time.Duration
}

type UserClaims struct {
	UserId uint `json:"user_id"`
	jwt.RegisteredClaims
}

func NewJwt(key string, tokenExpireDuration time.Duration) *Jwt {
	return &Jwt{Key: []byte(key), TokenExpireDuration: tokenExpireDuration}
}

func (s *Jwt) Validate() error {
	if s == nil || len(s.Key) < 32 {
		return errors.New("JWT key must contain at least 32 bytes")
	}
	if s.TokenExpireDuration < time.Second {
		return errors.New("token expiration must be at least one second")
	}
	return nil
}

// GenerateToken returns the signed token and its exact, fixed expiration time.
func (s *Jwt) GenerateToken(userId uint) (string, int64, error) {
	if err := s.Validate(); err != nil {
		return "", 0, err
	}
	if userId == 0 {
		return "", 0, errors.New("invalid user")
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", 0, err
	}
	now := time.Now()
	expires := jwt.NewNumericDate(now.Add(s.TokenExpireDuration))
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, UserClaims{
		UserId: userId,
		RegisteredClaims: jwt.RegisteredClaims{
			ID: id.String(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: expires,
		},
	})
	token, err := t.SignedString(s.Key)
	return token, expires.Unix(), err
}

func (s *Jwt) ParseToken(tokenString string) (uint, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	token, err := jwt.ParseWithClaims(tokenString, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
		return s.Key, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return 0, err
	}
	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid || claims.UserId == 0 || claims.ID == "" || claims.IssuedAt == nil {
		return 0, errors.New("invalid token claims")
	}
	return claims.UserId, nil
}
