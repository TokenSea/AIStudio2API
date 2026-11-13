package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestIORunRootRoutesReachWorker(t *testing.T) {
	t.Setenv("IORUN_BINDING_FILE", "fixture")
	called := 0
	h := rootHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
	}))
	for _, path := range []string{"/readiness", "/iorun/studio-readiness", "/generate/gemini-pro", "/generate/studio"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusForbidden || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("worker route rendered HTML: %s", path)
		}
	}
	if called != 4 {
		t.Fatal("worker route missing")
	}
}

type iorunUnchangedCatalog struct{ modelCatalogService }

func (iorunUnchangedCatalog) RefreshAccountState(context.Context, string, bool) (aistudio.AccountRefresh, error) {
	return aistudio.AccountRefresh{PreviousTier: aistudio.BenefitTierPro, Tier: aistudio.BenefitTierPro}, nil
}
func (iorunUnchangedCatalog) CachedModels() []aistudio.Model {
	return []aistudio.Model{{ID: "gemini-3-pro-image"}}
}
func TestIORunReadonlyCatalogAfterRestart(t *testing.T) {
	t.Setenv("IORUN_BINDING_FILE", "fixture")
	service := &trackedService{lifecycle: context.Background(), timeout: time.Second, catalog: iorunUnchangedCatalog{}}
	if _, err := service.Models(context.Background()); err == nil {
		t.Fatal("fixture must start without runtime catalog")
	}
	if _, err := service.refreshAccountState(context.Background(), "fixture@google.invalid", true); err != nil {
		t.Fatal(err)
	}
	models := service.modelSnapshot()
	if len(models) != 1 || models[0].ID != "gemini-3-pro-image" {
		t.Fatal("unchanged persisted catalog was not restored")
	}
	if service.state.Load() != serviceStopped {
		t.Fatal("inspection started generation service")
	}
}
