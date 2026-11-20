package ticketcrypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestSignAndVerifyCanonicalTicket(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := Data{Version: 1, TicketID: "ticket", BookingID: "booking", EventID: "event", TicketTypeID: "type", OwnerID: "owner"}
	hash, signature, err := Sign(data, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(data, hash, signature, publicKey) {
		t.Fatal("valid ticket did not verify")
	}
	data.OwnerID = "attacker"
	if Verify(data, hash, signature, publicKey) {
		t.Fatal("modified ticket verified")
	}
}

func TestHashOnlyRequiresSignatureForVerification(t *testing.T) {
	data := Data{Version: 1, TicketID: "ticket", BookingID: "booking", EventID: "event", TicketTypeID: "type", OwnerID: "owner"}
	hash, _, err := Sign(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if Verify(data, hash, "", publicKey) {
		t.Fatal("unsigned ticket verified")
	}
}
