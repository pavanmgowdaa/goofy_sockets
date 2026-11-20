package ticketcrypto

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Data is the versioned canonical representation hashed and signed for a ticket.
// Keep field order and names stable; changing them requires a new version.
type Data struct {
	Version      int    `json:"version"`
	TicketID     string `json:"ticket_id"`
	BookingID    string `json:"booking_id"`
	EventID      string `json:"event_id"`
	TicketTypeID string `json:"ticket_type_id"`
	OwnerID      string `json:"owner_id"`
}

func Canonical(data Data) ([]byte, error) {
	if data.Version != 1 || data.TicketID == "" || data.BookingID == "" || data.EventID == "" || data.TicketTypeID == "" || data.OwnerID == "" {
		return nil, errors.New("incomplete or unsupported ticket data")
	}
	return json.Marshal(data)
}

func Hash(data Data) (string, []byte, error) {
	canonical, err := Canonical(data)
	if err != nil {
		return "", nil, err
	}
	input := append([]byte("FAIRPASS-TICKET-V1\x00"), canonical...)
	digest := sha256.Sum256(input)
	return hex.EncodeToString(digest[:]), canonical, nil
}

func Sign(data Data, privateKey ed25519.PrivateKey) (hash, signature string, err error) {
	hash, canonical, err := Hash(data)
	if err != nil {
		return "", "", err
	}
	if len(privateKey) == 0 {
		return hash, "", nil
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", "", errors.New("ticket signing key has invalid length")
	}
	message := append([]byte("FAIRPASS-TICKET-SIGNATURE-V1\x00"), canonical...)
	return hash, hex.EncodeToString(ed25519.Sign(privateKey, message)), nil
}

func Verify(data Data, expectedHash, signature string, publicKey ed25519.PublicKey) bool {
	hash, canonical, err := Hash(data)
	if err != nil || hash != expectedHash || signature == "" || len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	message := append([]byte("FAIRPASS-TICKET-SIGNATURE-V1\x00"), canonical...)
	return ed25519.Verify(publicKey, message, sig)
}
