package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/managed"
	"github.com/google/uuid"
)

type iorunFake struct {
	aistudio.Service
	AdminService
	calls                 atomic.Int32
	tier                  string
	modes                 []int64
	sizes                 []string
	zero, fail, estimated bool
	delay                 time.Duration
	started               chan struct{}
}

func (f *iorunFake) RecordAccessStart(AccessLog) {}
func (f *iorunFake) RecordAccessLog(AccessLog)   {}

func (f *iorunFake) Accounts(context.Context) ([]AdminAccount, error) {
	return []AdminAccount{{ID: "fixture@google.invalid", Enabled: true, State: "ready", BenefitTier: f.tier, Models: []string{"gemini-3-pro-image"}}}, nil
}
func (f *iorunFake) RefreshAccount(ctx context.Context, id string) (AdminAccountRefresh, error) {
	a, _ := f.Accounts(ctx)
	return AdminAccountRefresh{Account: a[0]}, nil
}
func (f *iorunFake) Models(context.Context) ([]aistudio.Model, error) {
	return []aistudio.Model{{ID: "gemini-3-pro-image", AccessModes: f.modes, Methods: []string{"generateContent"}, Capabilities: map[string]bool{"image_route": true}, CapabilityOptions: map[string][]string{"image_output_resolutions": f.sizes, "image_aspect_ratios": {"1:1"}}}}, nil
}
func (f *iorunFake) Generate(ctx context.Context, r aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	if err := managed.Claim(ctx, "GenerateContent", r.AccountID, r.Model); err != nil {
		return nil, err
	}
	f.calls.Add(1)
	if f.started != nil {
		close(f.started)
	}
	out := make(chan aistudio.Event, 4)
	go func() {
		defer close(out)
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		if ctx.Err() != nil {
			out <- aistudio.Event{Kind: aistudio.EventError, Err: ctx.Err()}
			return
		}
		if f.fail {
			out <- aistudio.Event{Kind: aistudio.EventError, Err: errors.New("simulated transport break")}
			return
		}
		if !f.zero {
			n := map[string]int{"1K": 1024, "2K": 2048, "4K": 4096}[r.Config.ImageConfig.ImageSize]
			var b bytes.Buffer
			_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, n, n)))
			out <- aistudio.Event{Kind: aistudio.EventMedia, ProviderModel: r.Model, Part: aistudio.Part{InlineData: &aistudio.Blob{MIME: "image/png", Data: b.Bytes()}}}
		}
		u := &aistudio.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, Details: &aistudio.UsageDetails{PromptTokensDetails: []aistudio.ModalityTokenCount{{Modality: "TEXT", TokenCount: 10}}, CandidatesTokensDetails: []aistudio.ModalityTokenCount{{Modality: "IMAGE", TokenCount: 20}}}}
		if f.estimated {
			u.Origin = "estimated"
		}
		out <- aistudio.Event{Kind: aistudio.EventUsage, Usage: u}
		out <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	}()
	return out, nil
}
func iorunFixture(t *testing.T) (*iorunFake, IORunBinding, http.Handler) {
	t.Helper()
	t.Setenv("IORUN_BINDING_FILE", "fixture")
	f := &iorunFake{tier: "Pro", modes: []int64{3}, sizes: []string{"1K", "2K", "4K"}}
	b := IORunBinding{Token: strings.Repeat("a", 40), AccountID: uuid.NewString(), GoogleAccount: "fixture@google.invalid", LegacyURL: "http://127.0.0.1:1", StudioEnabled: true, ReceiptDir: t.TempDir()}
	return f, b, NewHandler(f, Config{Admin: f, IORun: &b})
}
func iorunBody(size string) string {
	return `{"iorunWebImage":{"model":"gemini-3-pro-image","resolution":"` + size + `"},"contents":[{"role":"user","parts":[{"text":"PRIVATE FIXTURE PROMPT"}]}],"generationConfig":{"responseModalities":["TEXT","IMAGE"],"imageConfig":{"imageSize":"` + size + `","aspectRatio":"1:1"}}}`
}
func iorunRequest(b IORunBinding, id, path string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(iorunBody("1K")))
	r.Header.Set("Authorization", "Bearer "+b.Token)
	r.Header.Set("X-IORun-Account-ID", b.AccountID)
	r.Header.Set("X-IORun-Request-ID", id)
	r.Header.Set("X-IORun-Deadline", time.Now().Add(time.Minute).Format(time.RFC3339Nano))
	return r
}
func TestIORunSyncSizesAndOriginals(t *testing.T) {
	for _, size := range []string{"1K", "2K", "4K"} {
		t.Run(size, func(t *testing.T) {
			f, b, h := iorunFixture(t)
			id := uuid.NewString()
			r := iorunRequest(b, id, "/iorun/generate/studio")
			r.Body = io.NopCloser(strings.NewReader(iorunBody(size)))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || w.Header().Get("X-IORun-Image-Resolution") != size || !bytes.Contains(w.Body.Bytes(), []byte("inlineData")) {
				t.Fatalf("sync status %d", w.Code)
			}
			repeat := httptest.NewRecorder()
			h.ServeHTTP(repeat, iorunRequest(b, id, "/iorun/generate/studio"))
			if repeat.Code != 409 || f.calls.Load() != 1 {
				t.Fatal("duplicate dispatched")
			}
			data, err := os.ReadFile(filepath.Join(b.ReceiptDir, id+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("PRIVATE")) || bytes.Contains(data, []byte("inlineData")) {
				t.Fatal("body persisted")
			}
			var receipt iorunReceipt
			_ = json.Unmarshal(data, &receipt)
			if receipt.State != "finished" || receipt.ImageCount == nil || *receipt.ImageCount != 1 {
				t.Fatal("receipt")
			}
		})
	}
}
func TestIORunAdmissionAndUsage(t *testing.T) {
	for _, mode := range []string{"free", "paid_only", "unknown_access", "unsupported_size", "wrong_account", "missing_key", "paused", "zero", "estimated"} {
		t.Run(mode, func(t *testing.T) {
			f, b, h := iorunFixture(t)
			r := iorunRequest(b, uuid.NewString(), "/iorun/generate/studio")
			switch mode {
			case "free":
				f.tier = "Free"
			case "paid_only":
				f.modes = []int64{1}
			case "unknown_access":
				f.modes = nil
			case "unsupported_size":
				f.sizes = []string{"4K"}
			case "wrong_account":
				r.Header.Set("X-IORun-Account-ID", uuid.NewString())
			case "missing_key":
				r.Header.Del("Authorization")
			case "paused":
				b.StudioEnabled = false
				h = NewHandler(f, Config{Admin: f, IORun: &b})
			case "zero":
				f.zero = true
			case "estimated":
				f.estimated = true
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if mode == "zero" || mode == "estimated" {
				if w.Code != 200 || f.calls.Load() != 1 {
					t.Fatal("valid result")
				}
				if mode == "estimated" && bytes.Contains(w.Body.Bytes(), []byte("usageMetadata")) {
					t.Fatal("estimate exposed as actual")
				}
				if mode == "zero" && bytes.Contains(w.Body.Bytes(), []byte("inlineData")) {
					t.Fatal("zero fabricated image")
				}
			} else if w.Code < 400 || f.calls.Load() != 0 {
				t.Fatalf("admission failed %d", w.Code)
			}
		})
	}
}
func TestIORunDisconnectAndRestart(t *testing.T) {
	f, b, h := iorunFixture(t)
	f.delay = 60 * time.Millisecond
	f.started = make(chan struct{})
	id := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	r := iorunRequest(b, id, "/iorun/generate/studio").WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	<-f.started
	cancel()
	<-done
	f.started = nil
	if f.calls.Load() != 1 || w.Code != 200 {
		t.Fatal("disconnect canceled generation")
	}
	h = NewHandler(f, Config{Admin: f, IORun: &b})
	q := iorunRequest(b, id, "/iorun/receipts/"+id)
	q.Method = "GET"
	result := httptest.NewRecorder()
	h.ServeHTTP(result, q)
	if result.Code != 200 || bytes.Contains(result.Body.Bytes(), []byte("inlineData")) {
		t.Fatal("metadata query")
	}
	f.fail = true
	w = httptest.NewRecorder()
	failed := uuid.NewString()
	h.ServeHTTP(w, iorunRequest(b, failed, "/iorun/generate/studio"))
	if w.Code != 502 {
		t.Fatal("unknown result")
	}
	h = NewHandler(f, Config{Admin: f, IORun: &b})
	next := httptest.NewRecorder()
	h.ServeHTTP(next, iorunRequest(b, uuid.NewString(), "/iorun/generate/studio"))
	if next.Code != 503 || f.calls.Load() != 2 {
		t.Fatal("restart resent unknown")
	}
	q = iorunRequest(b, failed, "/iorun/receipts/"+failed)
	q.Method = "GET"
	q.Header.Set("X-IORun-Account-ID", uuid.NewString())
	next = httptest.NewRecorder()
	h.ServeHTTP(next, q)
	if next.Code != 403 {
		t.Fatal("cross account receipt")
	}
}
func TestIORunLegacyUnifiedRouteAndBypass(t *testing.T) {
	f, b, _ := iorunFixture(t)
	var calls atomic.Int32
	var pngBytes bytes.Buffer
	_ = png.Encode(&pngBytes, image.NewGray(image.Rect(0, 0, 1024, 1024)))
	encoded := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	body := strings.ReplaceAll(iorunBody("1K"), "gemini-3-pro-image", "gemini-web-nano-banana-pro")
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+b.Token || r.Header.Get("X-IORun-Account-ID") != b.AccountID {
			t.Error("legacy binding")
		}
		calls.Add(1)
		actual, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/generate/gemini-pro" || string(actual) != body {
			t.Error("legacy request changed")
		}
		w.Header().Set("X-IORun-Image-Source", "google-gemini-web")
		w.Header().Set("X-IORun-Image-Model", "gemini-web-nano-banana-pro")
		w.Header().Set("X-IORun-Image-Resolution", "1K")
		iorunReply(w, 200, map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]string{"mimeType": "image/png", "data": encoded}}}}}}})
	}))
	defer legacy.Close()
	b.LegacyURL = legacy.URL
	h := NewHandler(f, Config{Admin: f, IORun: &b})
	w := httptest.NewRecorder()
	request := iorunRequest(b, uuid.NewString(), "/generate/gemini-pro")
	request.Body = io.NopCloser(strings.NewReader(body))
	h.ServeHTTP(w, request)
	if w.Code != 200 || calls.Load() != 1 || f.calls.Load() != 0 || !strings.Contains(w.Body.String(), encoded) || w.Header().Get("X-IORun-Image-Model") != "gemini-web-nano-banana-pro" || w.Header().Get("X-IORun-Image-Resolution") != "1K" {
		t.Fatal("legacy unified route")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, iorunRequest(b, uuid.NewString(), "/v1/images/generations"))
	if w.Code != 403 || f.calls.Load() != 0 {
		t.Fatal("public bypass")
	}
}

func TestIORunSocketDisconnectAndConcurrentDuplicate(t *testing.T) {
	f, b, h := iorunFixture(t)
	f.delay = 100 * time.Millisecond
	f.started = make(chan struct{})
	server := httptest.NewServer(h)
	defer server.Close()
	id := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := iorunRequest(b, id, "/generate/studio").WithContext(ctx)
	request.URL.Scheme, request.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
	request.RequestURI = ""
	done := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	<-f.started
	duplicate := httptest.NewRecorder()
	h.ServeHTTP(duplicate, iorunRequest(b, id, "/generate/studio"))
	if duplicate.Code != 409 {
		t.Fatal("concurrent duplicate accepted")
	}
	busy := httptest.NewRecorder()
	h.ServeHTTP(busy, iorunRequest(b, uuid.NewString(), "/generate/studio"))
	if busy.Code != 409 {
		t.Fatal("account slot not serialized")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("socket did not disconnect")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(b.ReceiptDir, id+".json"))
		var receipt iorunReceipt
		_ = json.Unmarshal(data, &receipt)
		if receipt.State == "finished" {
			if f.calls.Load() != 1 || receipt.ImageCount == nil || *receipt.ImageCount != 1 {
				t.Fatal("disconnected original invalid")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected original never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
