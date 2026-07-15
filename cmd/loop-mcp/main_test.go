package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestVerifyPaymentPreimage(t *testing.T) {
	preimage := strings.Repeat("01", 32)
	preimageBytes, err := hex.DecodeString(preimage)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(preimageBytes)
	paymentHash := hex.EncodeToString(hash[:])

	if !verifyPaymentPreimage(paymentHash, preimage) {
		t.Fatal("valid preimage was rejected")
	}
	for _, invalid := range []string{"dev", "", "not-hex", strings.Repeat("02", 32)} {
		if verifyPaymentPreimage(paymentHash, invalid) {
			t.Fatalf("invalid preimage was accepted: %q", invalid)
		}
	}
}

func TestTokenBindsToolAndExpires(t *testing.T) {
	secret := "test-secret"
	paymentHash := strings.Repeat("a", 64)
	token := issueToken(secret, paymentHash, "json_validate", 1784150000)
	ph, tool, ok := verifyTokenAt(secret, token, 1784150001)
	if !ok || ph != paymentHash || tool != "json_validate" {
		t.Fatalf("valid token rejected: ok=%v ph=%s tool=%s", ok, ph, tool)
	}
	if _, _, ok := verifyTokenAt(secret, token, 1784236401); ok {
		t.Fatal("expired token was accepted")
	}
	if _, _, ok := verifyTokenAt("wrong-secret", token, 1784150001); ok {
		t.Fatal("wrong secret was accepted")
	}
}

func TestConsumePaymentPreimageRejectsReplay(t *testing.T) {
	preimage := strings.Repeat("03", 32)
	preimageBytes, err := hex.DecodeString(preimage)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(preimageBytes)
	paymentHash := hex.EncodeToString(hash[:])
	consumedPaymentHashes.Delete(paymentHash)

	if !consumePaymentPreimage(paymentHash, preimage) {
		t.Fatal("first valid payment proof was rejected")
	}
	if consumePaymentPreimage(paymentHash, preimage) {
		t.Fatal("replayed payment proof was accepted")
	}
}
