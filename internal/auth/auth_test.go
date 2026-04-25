package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestMakeJWT_Success_ReturnsHS256TokenWithExpectedClaims(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret"

	tokenString, err := MakeJWT(userID, secret, time.Minute)
	if err != nil {
		t.Fatalf("MakeJWT returned unexpected error: %v", err)
	}
	if tokenString == "" {
		t.Fatal("expected non-empty token string")
	}
	if strings.Count(tokenString, ".") != 2 {
		t.Fatalf("expected JWT format with 3 segments, got: %q", tokenString)
	}

	claims := &jwt.RegisteredClaims{}
	parsedToken, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return []byte(secret), nil
	})
	if err != nil {
		t.Fatalf("failed to parse generated token: %v", err)
	}
	if !parsedToken.Valid {
		t.Fatal("expected generated token to be valid")
	}
	if parsedToken.Method.Alg() != jwt.SigningMethodHS256.Alg() {
		t.Fatalf("expected signing method HS256, got %s", parsedToken.Method.Alg())
	}
	if claims.Subject != userID.String() {
		t.Fatalf("expected subject %q, got %q", userID.String(), claims.Subject)
	}
	if claims.Issuer != "chirpy-access" {
		t.Fatalf("expected issuer %q, got %q", "chirpy-access", claims.Issuer)
	}
}

func TestMakeJWT_ErrorOnNilUserID(t *testing.T) {
	_, err := MakeJWT(uuid.Nil, "test-secret", time.Minute)
	if err == nil {
		t.Fatal("expected error for nil user ID, got nil")
	}
	if !strings.Contains(err.Error(), "invalid user ID") {
		t.Fatalf("expected error to contain %q, got %v", "invalid user ID", err)
	}
}

func TestMakeJWT_ErrorOnInvalidTokenSecret(t *testing.T) {
	_, err := MakeJWT(uuid.New(), "", time.Minute)
	if err == nil {
		t.Fatal("expected error for empty token secret, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token secret") {
		t.Fatalf("expected error to contain %q, got %v", "invalid token secret", err)
	}
}

func TestValidateJWT_ValidTokenReturnsUUID(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret"

	tokenString, err := MakeJWT(userID, secret, time.Minute)
	if err != nil {
		t.Fatalf("MakeJWT returned unexpected error: %v", err)
	}

	gotID, err := ValidateJWT(tokenString, secret)
	if err != nil {
		t.Fatalf("ValidateJWT returned unexpected error: %v", err)
	}
	if gotID != userID {
		t.Fatalf("expected user ID %v, got %v", userID, gotID)
	}
}

func TestValidateJWT_InvalidTokenReturnsNilUUIDAndError(t *testing.T) {
	gotID, err := ValidateJWT("not-a-valid-jwt", "test-secret")
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if gotID != uuid.Nil {
		t.Fatalf("expected uuid.Nil, got %v", gotID)
	}
}

func TestValidateJWT_ExpiredTokenReturnsNilUUIDAndError(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret"

	tokenString, err := MakeJWT(userID, secret, -1*time.Minute)
	if err != nil {
		t.Fatalf("MakeJWT returned unexpected error: %v", err)
	}

	gotID, err := ValidateJWT(tokenString, secret)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if gotID != uuid.Nil {
		t.Fatalf("expected uuid.Nil, got %v", gotID)
	}
}

func TestValidateJWT_InvalidSignatureReturnsNilUUIDAndError(t *testing.T) {
	userID := uuid.New()

	tokenString, err := MakeJWT(userID, "secret-a", time.Minute)
	if err != nil {
		t.Fatalf("MakeJWT returned unexpected error: %v", err)
	}

	gotID, err := ValidateJWT(tokenString, "secret-b")
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
	if gotID != uuid.Nil {
		t.Fatalf("expected uuid.Nil, got %v", gotID)
	}
}

func TestValidateJWT_InvalidUUIDSubjectReturnsNilUUIDAndError(t *testing.T) {
	secret := "test-secret"
	claims := jwt.RegisteredClaims{
		Issuer:    "chirpy-access",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		Subject:   "not-a-uuid",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	gotID, err := ValidateJWT(tokenString, secret)
	if err == nil {
		t.Fatal("expected error for invalid UUID subject, got nil")
	}
	if gotID != uuid.Nil {
		t.Fatalf("expected uuid.Nil, got %v", gotID)
	}
}
