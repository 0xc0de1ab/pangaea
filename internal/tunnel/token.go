package tunnel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidToken  = errors.New("tunnel: invalid token")
	ErrTokenMismatch = errors.New("tunnel: token signature mismatch")
)

type TokenSigner struct {
	key []byte
}

func NewTokenSigner(key []byte) (*TokenSigner, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("%w: signing key is required", ErrInvalidToken)
	}
	return &TokenSigner{key: append([]byte(nil), key...)}, nil
}

func (s *TokenSigner) Sign(claims StreamTokenClaims) (string, error) {
	if s == nil || len(s.key) == 0 {
		return "", fmt.Errorf("%w: signer is nil", ErrInvalidToken)
	}
	if err := claims.Validate(time.Now().UTC()); err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	signature := s.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *TokenSigner) Verify(token string, now time.Time) (StreamTokenClaims, error) {
	if s == nil || len(s.key) == 0 {
		return StreamTokenClaims{}, fmt.Errorf("%w: signer is nil", ErrInvalidToken)
	}
	payload, signature, err := splitToken(token)
	if err != nil {
		return StreamTokenClaims{}, err
	}
	expected := s.sign(payload)
	if !hmac.Equal(signature, expected) {
		return StreamTokenClaims{}, ErrTokenMismatch
	}
	var claims StreamTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return StreamTokenClaims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if err := claims.Validate(now); err != nil {
		return StreamTokenClaims{}, err
	}
	return claims, nil
}

func (s *TokenSigner) VerifyForDescriptor(token string, desc StreamDescriptor, now time.Time) (StreamTokenClaims, error) {
	claims, err := s.Verify(token, now)
	if err != nil {
		return StreamTokenClaims{}, err
	}
	if err := claims.ValidateForDescriptor(desc, now); err != nil {
		return StreamTokenClaims{}, err
	}
	return claims, nil
}

func (s *TokenSigner) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func splitToken(token string) ([]byte, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil, fmt.Errorf("%w: expected payload.signature", ErrInvalidToken)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: payload decode failed", ErrInvalidToken)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: signature decode failed", ErrInvalidToken)
	}
	return payload, signature, nil
}
