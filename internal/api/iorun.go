package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/managed"
	"github.com/google/uuid"
)

// IORunBinding contains only this dedicated Google account's worker binding.
// Google cookies remain in the fork's private auth directory.
type IORunBinding struct {
	Token         string `json:"token"`
	AccountID     string `json:"account_id"`
	GoogleAccount string `json:"google_account"`
	LegacyURL     string `json:"legacy_url"`
	StudioEnabled bool   `json:"studio_enabled"`
	ReceiptDir    string `json:"receipt_dir"`
}

func LoadIORunBinding(path string) (*IORunBinding, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	var b IORunBinding
	if json.Unmarshal(data, &b) != nil || len(b.Token) < 32 || !iorunUUID(b.AccountID) || b.GoogleAccount == "" || !filepath.IsAbs(b.ReceiptDir) {
		return nil, errors.New("iorun_binding_invalid")
	}
	u, err := url.Parse(b.LegacyURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Path != "" {
		return nil, errors.New("iorun_legacy_binding_invalid")
	}
	b.GoogleAccount = strings.ToLower(b.GoogleAccount)
	return &b, nil
}

func iorunUUID(s string) bool { u, err := uuid.Parse(s); return err == nil && u.String() == s }

type iorunReceipt struct {
	ID          string `json:"id"`
	Account     string `json:"account_id"`
	Fingerprint string `json:"fingerprint"`
	Model       string `json:"model"`
	Resolution  string `json:"resolution"`
	State       string `json:"state"`
	ImageCount  *int   `json:"image_count,omitempty"`
	UsageState  string `json:"usage_state,omitempty"`
}

type iorunWorker struct {
	s      *server
	b      IORunBinding
	mu     sync.Mutex
	client *http.Client
}

func newIORunWorker(s *server, b IORunBinding) http.Handler {
	return &iorunWorker{s: s, b: b, client: &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 5 * time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func iorunReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func iorunError(w http.ResponseWriter, status int, code string) {
	iorunReply(w, status, map[string]any{"error": map[string]string{"code": code}})
}

func (h *iorunWorker) save(r iorunReceipt, exclusive bool) error {
	if err := os.MkdirAll(h.b.ReceiptDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(h.b.ReceiptDir, r.ID+".json")
	target := path + ".tmp"
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if exclusive {
		target = path
		flags = os.O_CREATE | os.O_WRONLY | os.O_EXCL
	}
	f, err := os.OpenFile(target, flags, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(r)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if !exclusive {
		if err = os.Rename(target, path); err != nil {
			return err
		}
	}
	dir, err := os.Open(h.b.ReceiptDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (h *iorunWorker) blocked() bool {
	files, err := os.ReadDir(h.b.ReceiptDir)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.b.ReceiptDir, file.Name()))
		if err != nil {
			return true
		}
		var r iorunReceipt
		if json.Unmarshal(data, &r) != nil || r.Account != h.b.AccountID || !iorunUUID(r.ID) || file.Name() != r.ID+".json" {
			return true
		}
		if r.State != "finished" && r.State != "failed_before_submit" {
			return true
		}
	}
	return false
}

func (h *iorunWorker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.b.Token)) != 1 || r.Header.Get("X-IORun-Account-ID") != h.b.AccountID {
		iorunError(w, 403, "worker_access_denied")
		return
	}
	w.Header().Set("X-IORun-Account-ID", h.b.AccountID)
	if r.Method == "POST" && r.URL.Path == "/generate/studio" && iorunUUID(r.Header.Get("X-IORun-Request-ID")) {
		if _, err := os.Stat(filepath.Join(h.b.ReceiptDir, r.Header.Get("X-IORun-Request-ID")+".json")); err == nil {
			iorunError(w, 409, "original_request_exists")
			return
		}
	}
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/receipts/") {
		id := strings.TrimPrefix(r.URL.Path, "/receipts/")
		if !iorunUUID(id) {
			iorunError(w, 404, "original_not_found")
			return
		}
		data, err := os.ReadFile(filepath.Join(h.b.ReceiptDir, id+".json"))
		if err != nil {
			iorunError(w, 404, "original_not_found")
			return
		}
		var receipt iorunReceipt
		if json.Unmarshal(data, &receipt) != nil || receipt.Account != h.b.AccountID {
			iorunError(w, 404, "original_not_found")
			return
		}
		iorunReply(w, 200, receipt)
		return
	}
	if !h.mu.TryLock() {
		iorunError(w, 409, "browser_busy")
		return
	}
	defer h.mu.Unlock()
	if h.blocked() {
		if r.Method == "GET" && r.URL.Path == "/readiness" && iorunUUID(r.Header.Get("X-IORun-Check-ID")) {
			iorunReply(w, 200, map[string]any{"version": 1, "check_id": r.Header.Get("X-IORun-Check-ID"), "account_id": h.b.AccountID, "state": "previous_request_unconfirmed", "source": "google-gemini-web"})
			return
		}
		iorunError(w, 503, "previous_request_unconfirmed")
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/readiness":
		if !iorunUUID(r.Header.Get("X-IORun-Check-ID")) {
			iorunError(w, 400, "invalid_check_id")
			return
		}
		h.legacy(w, r)
	case r.Method == "GET" && r.URL.Path == "/studio-readiness":
		state, models := h.studioState(r.Context())
		iorunReply(w, 200, map[string]any{"state": state, "source": "google-studio-web", "studio_enabled": h.b.StudioEnabled, "models": models})
	case r.Method == "POST" && r.URL.Path == "/generate/gemini-pro":
		h.legacy(w, r)
	case r.Method == "POST" && r.URL.Path == "/generate/studio":
		h.studio(w, r)
	default:
		iorunError(w, 404, "worker_route_disabled")
	}
}

// This is an internal adapter, not a second external API or fallback route.
func (h *iorunWorker) legacy(w http.ResponseWriter, r *http.Request) {
	up, err := http.NewRequestWithContext(r.Context(), r.Method, h.b.LegacyURL+r.URL.Path, r.Body)
	if err != nil {
		iorunError(w, 503, "legacy_unavailable")
		return
	}
	for _, key := range []string{"Authorization", "Content-Type", "X-IORun-Account-ID", "X-IORun-Request-ID", "X-IORun-Deadline", "X-IORun-Check-ID"} {
		if values, ok := r.Header[http.CanonicalHeaderKey(key)]; ok {
			up.Header[key] = values
		}
	}
	response, err := h.client.Do(up)
	if err != nil {
		iorunError(w, 503, "legacy_unavailable")
		return
	}
	defer response.Body.Close()
	for _, key := range []string{"Content-Type", "X-IORun-Account-ID", "X-IORun-Image-Source", "X-IORun-Image-Model", "X-IORun-Image-Resolution"} {
		if v := response.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (h *iorunWorker) studioState(ctx context.Context) (string, []aistudio.Model) {
	if h.s.config.Admin == nil {
		return "login_required", nil
	}
	accounts, err := h.s.config.Admin.Accounts(ctx)
	if err != nil {
		return "inspection_required", nil
	}
	var found bool
	for _, a := range accounts {
		if strings.ToLower(a.ID) == h.b.GoogleAccount {
			found = true
			if !a.Enabled || a.State == "auth_required" {
				return "login_required", nil
			}
			if a.State == "restricted" {
				return "permission_denied", nil
			}
		}
	}
	if !found {
		return "login_required", nil
	}
	// The managed Client refuses AcceptTerms. Refresh cannot grant permissions.
	refresh, err := h.s.config.Admin.RefreshAccount(ctx, h.b.GoogleAccount)
	if err != nil {
		return "inspection_required", nil
	}
	a := refresh.Account
	if strings.ToLower(a.ID) != h.b.GoogleAccount || !a.Enabled {
		return "account_mismatch", nil
	}
	if a.State != "ready" {
		return "inspection_required", nil
	}
	if a.BenefitTier != "Pro" {
		return "subscription_required", nil
	}
	models, err := h.s.service.Models(ctx)
	if err != nil {
		return "browser_unavailable", nil
	}
	out := []aistudio.Model{}
	for _, m := range models {
		if (m.ID == "gemini-3-pro-image" || m.ID == "gemini-nano-banana-2.1") && slices.Contains(a.Models, m.ID) && slices.Contains(m.AccessModes, int64(3)) && slices.Contains(m.Methods, "generateContent") && m.Capabilities["image_route"] && !m.Hidden {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return "studio_selection_unverified", out
	}
	return "ready", out
}

type iorunInput struct {
	Selection struct {
		Model      string `json:"model"`
		Resolution string `json:"resolution"`
	} `json:"iorunWebImage"`
	Contents []struct {
		Role  string `json:"role"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	Config struct {
		Modalities []string `json:"responseModalities"`
		Image      struct {
			Size  string `json:"imageSize"`
			Ratio string `json:"aspectRatio"`
		} `json:"imageConfig"`
	} `json:"generationConfig"`
}

func (h *iorunWorker) studio(w http.ResponseWriter, r *http.Request) {
	if !h.b.StudioEnabled {
		iorunError(w, 403, "generation_paused")
		return
	}
	id := r.Header.Get("X-IORun-Request-ID")
	deadline, err := time.Parse(time.RFC3339Nano, r.Header.Get("X-IORun-Deadline"))
	if !iorunUUID(id) || err != nil || !deadline.After(time.Now()) || deadline.After(time.Now().Add(5*time.Minute)) {
		iorunError(w, 400, "invalid_original")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(data) > 65536 {
		iorunError(w, 413, "input_limit")
		return
	}
	defer clear(data)
	var input iorunInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(input.Contents) != 1 || len(input.Contents[0].Parts) != 1 || input.Contents[0].Role != "user" || strings.TrimSpace(input.Contents[0].Parts[0].Text) == "" {
		iorunError(w, 400, "input_invalid")
		return
	}
	model, resolution := input.Selection.Model, input.Selection.Resolution
	dimension := map[string]int{"1K": 1024, "2K": 2048, "4K": 4096}[resolution]
	if (model != "gemini-3-pro-image" && model != "gemini-nano-banana-2.1") || dimension == 0 || input.Config.Image.Size != resolution || input.Config.Image.Ratio != "1:1" {
		iorunError(w, 400, "selection_invalid")
		return
	}
	if !(len(input.Config.Modalities) == 1 && input.Config.Modalities[0] == "IMAGE" || len(input.Config.Modalities) == 2 && slices.Contains(input.Config.Modalities, "IMAGE") && slices.Contains(input.Config.Modalities, "TEXT")) {
		iorunError(w, 400, "modalities_invalid")
		return
	}
	digest := sha256.Sum256(data)
	receipt := iorunReceipt{ID: id, Account: h.b.AccountID, Fingerprint: hex.EncodeToString(digest[:]), Model: model, Resolution: resolution, State: "prepared"}
	if err = h.save(receipt, true); err != nil {
		if os.IsExist(err) {
			iorunError(w, 409, "original_request_exists")
		} else {
			iorunError(w, 503, "receipt_unavailable")
		}
		return
	}
	ctx, cancel := context.WithDeadline(context.WithoutCancel(r.Context()), deadline)
	defer cancel()
	state, models := h.studioState(ctx)
	selectionOK := false
	for _, m := range models {
		if m.ID == model && slices.Contains(m.CapabilityOptions["image_output_resolutions"], resolution) && slices.Contains(m.CapabilityOptions["image_aspect_ratios"], "1:1") {
			selectionOK = true
		}
	}
	if state != "ready" || !selectionOK {
		receipt.State = "failed_before_submit"
		if h.save(receipt, false) != nil {
			iorunError(w, 503, "receipt_unavailable")
			return
		}
		iorunError(w, 503, "studio_subscription_or_selection_unverified")
		return
	}
	ctx, permit := managed.WithPermit(ctx, h.b.GoogleAccount, model, func() error { receipt.State = "dispatched"; return h.save(receipt, false) })
	store := false
	request := aistudio.GenerateRequest{ID: id, Model: model, AccountID: h.b.GoogleAccount, Store: &store, Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: input.Contents[0].Parts[0].Text}}}}, Config: aistudio.GenerationConfig{ResponseModalities: []aistudio.ResponseModality{aistudio.ResponseModalityImage, aistudio.ResponseModalityText}, ImageConfig: &aistudio.ImageConfig{ImageSize: resolution, AspectRatio: "1:1"}}}
	events, generateErr := h.s.service.Generate(ctx, request)
	parts := []any{}
	count := 0
	finished := false
	var usage *aistudio.Usage
	if generateErr == nil {
	loop:
		for {
			select {
			case <-ctx.Done():
				generateErr = ctx.Err()
				break loop
			case e, ok := <-events:
				if !ok {
					break loop
				}
				if e.ProviderModel != "" && e.ProviderModel != model {
					generateErr = errors.New("model_mismatch")
					cancel()
					break loop
				}
				switch e.Kind {
				case aistudio.EventError:
					generateErr = e.Err
					if generateErr == nil {
						generateErr = errors.New("upstream_error")
					}
				case aistudio.EventUsage:
					usage = e.Usage
				case aistudio.EventFinish:
					finished = true
				case aistudio.EventMedia:
					if e.Thought || e.InlineData == nil {
						continue
					}
					blob := e.InlineData
					count++
					if count > 1 || len(blob.Data) > 10<<20 || !slices.Contains([]string{"image/png", "image/jpeg", "image/webp"}, blob.MIME) {
						generateErr = errors.New("image_invalid")
						continue
					}
					cfg, format, err := image.DecodeConfig(bytes.NewReader(blob.Data))
					if err != nil || "image/"+format != blob.MIME || cfg.Width != dimension || cfg.Height != dimension {
						generateErr = errors.New("image_size_mismatch")
						continue
					}
					if _, _, err = image.Decode(bytes.NewReader(blob.Data)); err != nil {
						generateErr = errors.New("image_invalid")
						continue
					}
					parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": blob.MIME, "data": base64.StdEncoding.EncodeToString(blob.Data)}})
				}
			}
		}
	}
	if generateErr != nil || !finished {
		receipt.State = "unconfirmed"
		if !permit.Claimed() {
			receipt.State = "failed_before_submit"
		}
		_ = h.save(receipt, false)
		iorunError(w, 502, "studio_response_unconfirmed")
		return
	}
	receipt.State = "finished"
	receipt.ImageCount = &count
	receipt.UsageState = "usage_unconfirmed"
	response := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": "STOP"}}}
	if usage != nil && usage.Origin == "" && !usage.OutputTokensMissing && usage.Details != nil && len(usage.Details.PromptTokensDetails) > 0 && len(usage.Details.CandidatesTokensDetails) > 0 {
		response["usageMetadata"] = map[string]any{"promptTokenCount": usage.InputTokens, "candidatesTokenCount": usage.OutputTokens, "totalTokenCount": usage.TotalTokens, "thoughtsTokenCount": usage.ReasoningTokens, "cachedContentTokenCount": usage.CachedTokens, "promptTokensDetails": usage.Details.PromptTokensDetails, "candidatesTokensDetails": usage.Details.CandidatesTokensDetails}
		receipt.UsageState = "upstream_received"
	}
	if err = h.save(receipt, false); err != nil {
		iorunError(w, 503, "receipt_unavailable")
		return
	}
	w.Header().Set("X-IORun-Image-Source", "google-studio-web")
	w.Header().Set("X-IORun-Image-Model", model)
	w.Header().Set("X-IORun-Image-Resolution", resolution)
	iorunReply(w, 200, response)
}
