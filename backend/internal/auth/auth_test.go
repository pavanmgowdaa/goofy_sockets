package auth

import (
	"testing"
	"time"
)

func TestPasswordHashAndCheck(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct horse battery staple") {
		t.Fatal("expected password to verify")
	}
	if CheckPassword(hash, "wrong password") {
		t.Fatal("unexpected password match")
	}
	if _, err = HashPassword("short"); err == nil {
		t.Fatal("expected short password rejection")
	}
}

func TestTokenRoundTrip(t *testing.T) {
	token, err := Token("0123456789abcdef0123456789abcdef", "user-1", "organizer", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Parse("0123456789abcdef0123456789abcdef", token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" || claims.Role != "organizer" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err = Parse("wrong-secret", token); err == nil {
		t.Fatal("expected signature validation failure")
	}
}

func TestSHA256Hex(t *testing.T) {
	got := SHA256Hex([]byte("abc"))
	const expected = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != expected {
		t.Fatalf("SHA256Hex(abc) = %s; want %s", got, expected)
	}
}
