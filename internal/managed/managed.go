// Package managed constrains the fork when hosted as an IORun worker.
package managed

import (
	"context"
	"errors"
	"os"
	"sync"
)

var ErrDenied = errors.New("iorun_managed_dispatch_denied")

func Enabled() bool { return os.Getenv("IORUN_BINDING_FILE") != "" }

type key struct{}
type Permit struct {
	mu             sync.Mutex
	account, model string
	claimed        bool
	commit         func() error
}

func WithPermit(ctx context.Context, account, model string, commit func() error) (context.Context, *Permit) {
	p := &Permit{account: account, model: model, commit: commit}
	return context.WithValue(ctx, key{}, p), p
}

func (p *Permit) Claimed() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.claimed }

// Claim is called immediately before the actual upstream send, including every
// authentication replay, continuation or retry path. Durable commit runs first.
func Claim(ctx context.Context, method, account, model string) error {
	if !Enabled() {
		return nil
	}
	p, ok := ctx.Value(key{}).(*Permit)
	if !ok {
		return ErrDenied
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if method != "GenerateContent" || account != p.account || model != p.model || p.claimed {
		return ErrDenied
	}
	// A failed disk write is also terminal for this in-memory permit.
	p.claimed = true
	return p.commit()
}

// Managed workers never accept terms or invoke alternate generation APIs.
func AllowRPC(method string) bool {
	if !Enabled() {
		return true
	}
	switch method {
	case "GetLoggingContext", "GetAiStudioBenefitTier", "ListModels", "GetUserRestrictions", "GetUserPreferences", "ListPromos", "ListIncidentsHistory", "CountTokens", "GenerateAccessToken", "GetAppFolder":
		return true
	default:
		return false
	}
}
