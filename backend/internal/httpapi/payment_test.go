package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyRazorpaySignature(t *testing.T) {
	body := []byte(`{"event":"payment.captured"}`)
	mac := hmac.New(sha256.New, []byte("webhook-secret"))
	_, _ = mac.Write(body)
	valid := hex.EncodeToString(mac.Sum(nil))
	if !verifyRazorpaySignature(body, valid, "webhook-secret") {
		t.Fatal("valid signature rejected")
	}
	if verifyRazorpaySignature(append(body, ' '), valid, "webhook-secret") {
		t.Fatal("signature accepted for modified payload")
	}
	if verifyRazorpaySignature(body, valid, "wrong-secret") {
		t.Fatal("signature accepted for wrong secret")
	}
	if verifyRazorpaySignature(body, valid, "") {
		t.Fatal("signature accepted with empty secret")
	}
}
