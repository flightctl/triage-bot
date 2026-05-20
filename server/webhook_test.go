package server

import (
	"testing"
)

func TestVerifySignature_Valid(t *testing.T) {
	secret := "my-webhook-secret"
	body := []byte(`{"webhookEvent":"jira:issue_updated","issue":{"key":"PROJ-123"}}`)
	header := ComputeSignature(secret, body)

	if !VerifySignature(secret, header, body) {
		t.Error("expected valid signature to pass verification")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	secret := "my-webhook-secret"
	body := []byte(`{"webhookEvent":"jira:issue_updated"}`)

	if VerifySignature(secret, "sha256=deadbeef", body) {
		t.Error("expected invalid signature to fail verification")
	}
}

func TestVerifySignature_EmptyHeader(t *testing.T) {
	if VerifySignature("secret", "", []byte("body")) {
		t.Error("expected empty header to fail verification")
	}
}

func TestVerifySignature_WrongFormat(t *testing.T) {
	if VerifySignature("secret", "md5=abc123", []byte("body")) {
		t.Error("expected non-sha256 method to fail verification")
	}
}

func TestVerifySignature_TamperedBody(t *testing.T) {
	secret := "my-webhook-secret"
	original := []byte(`{"issue":{"key":"PROJ-123"}}`)
	header := ComputeSignature(secret, original)

	tampered := []byte(`{"issue":{"key":"PROJ-999"}}`)
	if VerifySignature(secret, header, tampered) {
		t.Error("expected tampered body to fail verification")
	}
}
