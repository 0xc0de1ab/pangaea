package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/antigravity-compat-proxy/internal/interfaces"
)

func TestCloudStreamProxyForwardsStreamRequestWithoutMutation(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotQuery string
	var gotBody string
	var gotAuthorization string
	var gotContentType string
	var gotCSRF string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuthorization = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotCSRF = r.Header.Get("X-Codeium-Csrf-Token")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}}\n\n")
	}))
	defer upstream.Close()

	proxy, err := NewCloudStreamProxy("127.0.0.1:0", upstream.URL+"/cloud")
	if err != nil {
		t.Fatal(err)
	}
	capturePath := filepath.Join(t.TempDir(), "capture.jsonl")
	proxy.SetCapturePath(capturePath)
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	defer proxy.Close(context.Background())

	body := `{"model":"gemini-3-flash","request":{"contents":[{"parts":[{"text":"hello prompt"}],"role":"user"}]}}`
	req, err := http.NewRequest(http.MethodPost, proxy.EndpointURL()+"/v1internal:streamGenerateContent?alt=sse&case=exact", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer exact-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Codeium-Csrf-Token", "csrf-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(responseBody))
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method mutated: %s", gotMethod)
	}
	if gotPath != "/cloud/v1internal:streamGenerateContent" {
		t.Fatalf("path mutated: %s", gotPath)
	}
	if gotQuery != "alt=sse&case=exact" {
		t.Fatalf("query mutated: %s", gotQuery)
	}
	if gotBody != body {
		t.Fatalf("body mutated:\nwant: %s\n got: %s", body, gotBody)
	}
	if gotAuthorization != "Bearer exact-token" {
		t.Fatalf("authorization header was not forwarded: %q", gotAuthorization)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type header was not forwarded: %q", gotContentType)
	}
	if gotCSRF != "csrf-token" {
		t.Fatalf("csrf header was not forwarded: %q", gotCSRF)
	}
	if string(responseBody) != "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}}\n\n" {
		t.Fatalf("response stream mutated: %q", string(responseBody))
	}

	captureBytes, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	var rec cloudCaptureRecord
	if err := json.Unmarshal(bytes.TrimSpace(captureBytes), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.RequestBody != body {
		t.Fatalf("capture body mismatch: %s", rec.RequestBody)
	}
	if rec.RequestBodySHA256 != sha256Hex([]byte(body)) {
		t.Fatalf("capture request hash mismatch: %s", rec.RequestBodySHA256)
	}
	if rec.ResponseStatus != http.StatusOK {
		t.Fatalf("capture status mismatch: %d", rec.ResponseStatus)
	}
	if rec.ResponseSSEEvents != 1 {
		t.Fatalf("capture SSE event count mismatch: %d", rec.ResponseSSEEvents)
	}
	if values := rec.RequestHeaders["Authorization"]; len(values) != 1 || values[0] != "<redacted>" {
		t.Fatalf("authorization was not redacted in capture: %#v", values)
	}
}

func TestCloudStreamProxyTapsSSETextChunks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:streamGenerateContent" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "{}")
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "hello prompt") {
			t.Fatalf("proxy did not forward request body: %s", string(body))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"text\":\"hidden\"}]}}]}}\n\n")
		fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello \"}]}}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1,\"totalTokenCount\":3}}}\n\n")
		fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"world\"}]}}]}}\n\n")
	}))
	defer upstream.Close()

	proxy, err := NewCloudStreamProxy("127.0.0.1:0", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	defer proxy.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, cleanup := proxy.Subscribe(ctx, "hello prompt")
	defer cleanup()

	resp, err := http.Post(proxy.EndpointURL()+"/v1internal:streamGenerateContent?alt=sse", "application/json", strings.NewReader(`{"prompt":"hello prompt"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	_, _ = io.ReadAll(resp.Body)

	var got []string
	var sawUsage bool
	for chunk := range ch {
		if chunk.Content != "" {
			got = append(got, chunk.Content)
		}
		if chunk.Usage != nil && chunk.Usage.TotalTokens == 3 {
			sawUsage = true
		}
	}
	if strings.Join(got, "") != "hello world" {
		t.Fatalf("unexpected chunks: %#v", got)
	}
	if !sawUsage {
		t.Fatal("expected usage metadata")
	}
}

func TestCloudStreamProxyRetriesTransientStreamStatus(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"code":429,"message":"rate limited","details":[{"metadata":{"quotaResetDelay":"1ms"}}]}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"after retry\"}]}}]}}\n\n")
	}))
	defer upstream.Close()

	proxy, err := NewCloudStreamProxy("127.0.0.1:0", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy.SetStreamRetryPolicy(2, time.Millisecond, time.Millisecond)
	capturePath := filepath.Join(t.TempDir(), "capture.jsonl")
	proxy.SetCapturePath(capturePath)
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	defer proxy.Close(context.Background())

	resp, err := http.Post(proxy.EndpointURL()+"/v1internal:streamGenerateContent?alt=sse", "application/json", strings.NewReader(`{"prompt":"retry"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected retry to recover with 200, got %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "after retry") {
		t.Fatalf("missing successful retried response: %s", string(body))
	}
	if attempts != 2 {
		t.Fatalf("expected 2 upstream attempts, got %d", attempts)
	}

	lines := strings.Split(strings.TrimSpace(string(mustReadFile(t, capturePath))), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected retry and success capture records, got %d", len(lines))
	}
	var first cloudCaptureRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.ResponseStatus != http.StatusTooManyRequests || first.RetryAttempt != 1 || !first.Retryable {
		t.Fatalf("unexpected retry capture: %#v", first)
	}
	var second cloudCaptureRecord
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second.ResponseStatus != http.StatusOK || second.ResponseSSEEvents != 1 {
		t.Fatalf("unexpected success capture: %#v", second)
	}
}

func TestAntigravityStreamCapture100Fixture(t *testing.T) {
	file, err := os.Open(filepath.Join("testdata", "ag_stream_capture_100.normalized.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	coverage := map[string]bool{
		"system prompt":    false,
		"continuation":     false,
		"tool schema":      false,
		"tool result":      false,
		"image attachment": false,
		"file attachment":  false,
		"mcp":              false,
		"long context":     false,
		"markdown table":   false,
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 128*1024), 4*1024*1024)
	count := 0
	for scanner.Scan() {
		count++
		var rec streamCaptureFixtureRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("case %d: invalid fixture JSON: %v", count, err)
		}
		validateStreamCaptureFixtureRecord(t, count, rec, "gemini-3-flash", true)
		markStreamCoverage(coverage, rec.RequestBody)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("expected 100 stream captures, got %d", count)
	}
	for name, ok := range coverage {
		if !ok {
			t.Fatalf("fixture does not cover %s", name)
		}
	}
}

func TestAntigravityStreamCapture50ModelMatrixFixture(t *testing.T) {
	file, err := os.Open(filepath.Join("testdata", "ag_stream_capture_50_models.normalized.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	protocols := map[string]bool{"openai": false, "anthropic": false, "gemini": false}
	kinds := map[string]bool{
		"simple":       false,
		"system":       false,
		"continuation": false,
		"tool_schema":  false,
		"tool_result":  false,
		"image":        false,
		"file":         false,
		"mcp":          false,
		"long_context": false,
		"markdown":     false,
	}
	models := map[string]bool{}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 128*1024), 4*1024*1024)
	count := 0
	for scanner.Scan() {
		count++
		var rec streamCaptureFixtureRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("case %d: invalid fixture JSON: %v", count, err)
		}
		if rec.Protocol == "" {
			t.Fatalf("case %d: missing protocol metadata", count)
		}
		if rec.Kind == "" {
			t.Fatalf("case %d: missing kind metadata", count)
		}
		if rec.RequestedModel == "" {
			t.Fatalf("case %d: missing requested model metadata", count)
		}
		validateStreamCaptureFixtureRecord(t, count, rec, rec.RequestedModel, false)
		if _, ok := protocols[rec.Protocol]; !ok {
			t.Fatalf("case %d: unexpected protocol %q", count, rec.Protocol)
		}
		if _, ok := kinds[rec.Kind]; !ok {
			t.Fatalf("case %d: unexpected kind %q", count, rec.Kind)
		}
		protocols[rec.Protocol] = true
		kinds[rec.Kind] = true
		models[rec.RequestedModel] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 50 {
		t.Fatalf("expected 50 model-matrix stream captures, got %d", count)
	}
	if len(models) < 10 {
		t.Fatalf("expected at least 10 distinct requested models, got %d: %#v", len(models), models)
	}
	for protocol, ok := range protocols {
		if !ok {
			t.Fatalf("fixture does not cover %s protocol", protocol)
		}
	}
	for kind, ok := range kinds {
		if !ok {
			t.Fatalf("fixture does not cover %s requests", kind)
		}
	}
}

type streamCaptureFixtureRecord struct {
	Case               int    `json:"case"`
	ClientIndex        int    `json:"client_index,omitempty"`
	Protocol           string `json:"protocol,omitempty"`
	Kind               string `json:"kind,omitempty"`
	RequestedModel     string `json:"requested_model,omitempty"`
	Method             string `json:"method"`
	Path               string `json:"path"`
	Query              string `json:"query"`
	RequestBody        string `json:"request_body"`
	RequestBodySHA256  string `json:"request_body_sha256"`
	ResponseBody       string `json:"response_body"`
	ResponseBodySHA256 string `json:"response_body_sha256"`
	ResponseSSEEvents  int    `json:"response_sse_events"`
	ResponseStatus     int    `json:"response_status"`
}

func validateStreamCaptureFixtureRecord(t *testing.T, index int, rec streamCaptureFixtureRecord, expectedModel string, requireThinking bool) {
	t.Helper()
	if rec.Case != index {
		t.Fatalf("case %d: expected sequential case id, got %d", index, rec.Case)
	}
	if rec.Method != http.MethodPost {
		t.Fatalf("case %d: method = %q", index, rec.Method)
	}
	if rec.Path != "/v1internal:streamGenerateContent" {
		t.Fatalf("case %d: path = %q", index, rec.Path)
	}
	if rec.Query != "alt=sse" {
		t.Fatalf("case %d: query = %q", index, rec.Query)
	}
	if rec.ResponseStatus != http.StatusOK {
		t.Fatalf("case %d: response status = %d", index, rec.ResponseStatus)
	}
	if rec.RequestBodySHA256 != sha256Hex([]byte(rec.RequestBody)) {
		t.Fatalf("case %d: request hash mismatch", index)
	}
	if rec.ResponseBodySHA256 != sha256Hex([]byte(rec.ResponseBody)) {
		t.Fatalf("case %d: response hash mismatch", index)
	}
	if rec.ResponseSSEEvents != countSSEEvents([]byte(rec.ResponseBody)) {
		t.Fatalf("case %d: response SSE event count mismatch", index)
	}
	if rec.ResponseSSEEvents < 2 {
		t.Fatalf("case %d: expected multiple SSE events, got %d", index, rec.ResponseSSEEvents)
	}

	var req streamGenerateContentRequest
	if err := json.Unmarshal([]byte(rec.RequestBody), &req); err != nil {
		t.Fatalf("case %d: request body is not JSON: %v", index, err)
	}
	if expectedModel != "" && req.Model != expectedModel {
		t.Fatalf("case %d: model = %q, want %q", index, req.Model, expectedModel)
	}
	if req.Project != "<project>" {
		t.Fatalf("case %d: project was not normalized: %q", index, req.Project)
	}
	if req.RequestID != "chat/<request-id>" {
		t.Fatalf("case %d: request id was not normalized: %q", index, req.RequestID)
	}
	if req.RequestType != "chat" {
		t.Fatalf("case %d: request type = %q", index, req.RequestType)
	}
	if req.UserAgent != "antigravity" {
		t.Fatalf("case %d: user agent = %q", index, req.UserAgent)
	}
	if req.Request.SessionID != "<session-id>" {
		t.Fatalf("case %d: session id was not normalized: %q", index, req.Request.SessionID)
	}
	if requireThinking {
		if !req.Request.GenerationConfig.ThinkingConfig.IncludeThoughts {
			t.Fatalf("case %d: includeThoughts is false", index)
		}
		if req.Request.GenerationConfig.ThinkingConfig.ThinkingBudget != -1 {
			t.Fatalf("case %d: thinkingBudget = %d", index, req.Request.GenerationConfig.ThinkingConfig.ThinkingBudget)
		}
	}
	if len(req.Request.Contents) == 0 {
		t.Fatalf("case %d: no request contents", index)
	}
	if len(req.Request.SystemInstruction.Parts) == 0 || req.Request.SystemInstruction.Parts[0].Text == "" {
		t.Fatalf("case %d: missing system instruction", index)
	}

	contentChunks, usageChunks := replayCloudStreamResponse(t, index, rec.ResponseBody)
	if contentChunks == 0 {
		t.Fatalf("case %d: replay emitted no content chunks", index)
	}
	if usageChunks == 0 {
		t.Fatalf("case %d: replay emitted no usage chunks", index)
	}
}

type streamGenerateContentRequest struct {
	Model   string `json:"model"`
	Project string `json:"project"`
	Request struct {
		Contents []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
			Role string `json:"role"`
		} `json:"contents"`
		GenerationConfig struct {
			ThinkingConfig struct {
				IncludeThoughts bool `json:"includeThoughts"`
				ThinkingBudget  int  `json:"thinkingBudget"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
		SessionID         string `json:"sessionId"`
		SystemInstruction struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
			Role string `json:"role"`
		} `json:"systemInstruction"`
	} `json:"request"`
	RequestID   string `json:"requestId"`
	RequestType string `json:"requestType"`
	UserAgent   string `json:"userAgent"`
}

func replayCloudStreamResponse(t *testing.T, index int, body string) (int, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sub := &cloudStreamSubscription{
		ctx:    ctx,
		cancel: cancel,
		ch:     make(chan *interfaces.StreamChunk, 8192),
	}
	var data bytes.Buffer
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		processSSELine(append(scanner.Bytes(), '\n'), &data, sub)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("case %d: scan response body: %v", index, err)
	}
	processSSELine([]byte("\n"), &data, sub)
	sub.close()

	contentChunks := 0
	usageChunks := 0
	for chunk := range sub.ch {
		if chunk.Content != "" {
			contentChunks++
		}
		if chunk.Usage != nil {
			usageChunks++
		}
	}
	return contentChunks, usageChunks
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func markStreamCoverage(coverage map[string]bool, requestBody string) {
	if strings.Contains(requestBody, "[System]") {
		coverage["system prompt"] = true
	}
	if strings.Contains(requestBody, "[Assistant]") {
		coverage["continuation"] = true
	}
	if strings.Contains(requestBody, "tool_schema") || strings.Contains(requestBody, "lookup_go_version") {
		coverage["tool schema"] = true
	}
	if strings.Contains(requestBody, "<tool_call>") || strings.Contains(requestBody, "<observation>") {
		coverage["tool result"] = true
	}
	if strings.Contains(requestBody, "<image>") {
		coverage["image attachment"] = true
	}
	if strings.Contains(requestBody, "[file: main.go]") {
		coverage["file attachment"] = true
	}
	if strings.Contains(requestBody, "mcp") || strings.Contains(requestBody, "MCP") {
		coverage["mcp"] = true
	}
	if strings.Contains(requestBody, "constraint 14") {
		coverage["long context"] = true
	}
	if strings.Contains(requestBody, "markdown/code/table") {
		coverage["markdown table"] = true
	}
}
