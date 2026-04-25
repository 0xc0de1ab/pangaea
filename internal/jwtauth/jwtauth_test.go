package jwtauth

import (
	"path/filepath"
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token, err := Issue(secret, "node-a", []string{"p1", "p1", "p2"}, "issuer", "audience", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify(secret, token, "issuer", "audience", now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "node-a" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if len(claims.Profiles) != 2 || !claims.AllowsProfile("p1") || !claims.AllowsProfile("p2") {
		t.Fatalf("profiles = %#v", claims.Profiles)
	}
}

func TestWriteAndLoadSecretFile(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwt.secret")
	if err := WriteSecretFile(path, secret); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSecretFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(secret) {
		t.Fatalf("secret mismatch")
	}
}
