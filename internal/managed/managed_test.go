package managed

import (
	"context"
	"errors"
	"testing"
)

func TestPermitNoReplayOrCrossAccount(t *testing.T) {
	t.Setenv("IORUN_BINDING_FILE", "fixture")
	count := 0
	ctx, p := WithPermit(context.Background(), "google-account", "image-model", func() error { count++; return nil })
	if Claim(ctx, "GenerateContent", "other-account", "image-model") == nil {
		t.Fatal("cross account accepted")
	}
	if Claim(ctx, "GenerateContent", "google-account", "other-model") == nil {
		t.Fatal("cross model accepted")
	}
	if Claim(ctx, "ProxyUnaryCall", "google-account", "image-model") == nil {
		t.Fatal("alternate API accepted")
	}
	if err := Claim(ctx, "GenerateContent", "google-account", "image-model"); err != nil {
		t.Fatal(err)
	}
	if Claim(ctx, "GenerateContent", "google-account", "image-model") == nil || count != 1 || !p.Claimed() {
		t.Fatal("replay")
	}
	if Claim(context.Background(), "GenerateContent", "google-account", "image-model") == nil {
		t.Fatal("unbound generation")
	}
	if AllowRPC("AcceptTerms") || AllowRPC("CreateInteraction") || !AllowRPC("GetAiStudioBenefitTier") {
		t.Fatal("RPC boundary")
	}
}

func TestFailedCommitCannotRetry(t *testing.T) {
	t.Setenv("IORUN_BINDING_FILE", "fixture")
	count := 0
	ctx, _ := WithPermit(context.Background(), "a", "m", func() error { count++; return errors.New("disk failure") })
	if Claim(ctx, "GenerateContent", "a", "m") == nil || Claim(ctx, "GenerateContent", "a", "m") == nil || count != 1 {
		t.Fatal("failed receipt retried")
	}
}
