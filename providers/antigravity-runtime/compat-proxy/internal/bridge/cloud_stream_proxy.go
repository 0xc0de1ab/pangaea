package bridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/antigravity-compat-proxy/internal/interfaces"
	"github.com/google/antigravity-compat-proxy/internal/models"
)

const defaultCloudCodeEndpoint = "https://daily-cloudcode-pa.googleapis.com"

type CloudStreamProxy struct {
	addr     string
	upstream *url.URL
	client   *http.Client

	streamRetryAttempts int
	streamRetryBase     time.Duration
	streamRetryMax      time.Duration

	server *http.Server
	ln     net.Listener

	mu   sync.Mutex
	subs []*cloudStreamSubscription

	capturePath string
	captureMu   sync.Mutex
	captureSeq  uint64
}

type cloudStreamSubscription struct {
	prompt string
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan *interfaces.StreamChunk
	once   sync.Once
}

func NewCloudStreamProxy(addr string, upstream string) (*CloudStreamProxy, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "127.0.0.1:5599"
	}
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		upstream = defaultCloudCodeEndpoint
	}
	parsed, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("invalid cloud code endpoint %q: %w", upstream, err)
	}
	return &CloudStreamProxy{
		addr:                addr,
		upstream:            parsed,
		client:              &http.Client{},
		streamRetryAttempts: envInt("ANTIGRAVITY_STREAM_RETRY_ATTEMPTS", 3, 1, 8),
		streamRetryBase:     envDuration("ANTIGRAVITY_STREAM_RETRY_BASE_DELAY", 500*time.Millisecond),
		streamRetryMax:      envDuration("ANTIGRAVITY_STREAM_RETRY_MAX_DELAY", 5*time.Second),
		capturePath:         strings.TrimSpace(os.Getenv("ANTIGRAVITY_STREAM_CAPTURE_PATH")),
	}, nil
}

func (p *CloudStreamProxy) SetCapturePath(path string) {
	p.capturePath = strings.TrimSpace(path)
}

func (p *CloudStreamProxy) SetStreamRetryPolicy(attempts int, baseDelay time.Duration, maxDelay time.Duration) {
	if attempts < 1 {
		attempts = 1
	}
	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}
	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}
	p.streamRetryAttempts = attempts
	p.streamRetryBase = baseDelay
	p.streamRetryMax = maxDelay
}

func (p *CloudStreamProxy) Start() error {
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return err
	}
	p.ln = ln
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := p.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Printf("CloudCode stream proxy stopped unexpectedly: %v\n", err)
		}
	}()
	return nil
}

func (p *CloudStreamProxy) Close(ctx context.Context) error {
	if p.server == nil {
		return nil
	}
	return p.server.Shutdown(ctx)
}

func (p *CloudStreamProxy) EndpointURL() string {
	if p.ln == nil {
		return "http://" + p.addr
	}
	return "http://" + p.ln.Addr().String()
}

func (p *CloudStreamProxy) Subscribe(ctx context.Context, prompt string) (<-chan *interfaces.StreamChunk, func()) {
	subCtx, cancel := context.WithCancel(ctx)
	sub := &cloudStreamSubscription{
		prompt: prompt,
		ctx:    subCtx,
		cancel: cancel,
		ch:     make(chan *interfaces.StreamChunk, 64),
	}
	p.mu.Lock()
	p.subs = append(p.subs, sub)
	p.mu.Unlock()

	cleanup := func() {
		p.removeSub(sub)
		sub.close()
	}
	go func() {
		<-subCtx.Done()
		cleanup()
	}()
	return sub.ch, cleanup
}

func (p *CloudStreamProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	upstreamURL := *p.upstream
	upstreamURL.Path = joinURLPath(p.upstream.Path, r.URL.Path)
	upstreamURL.RawQuery = r.URL.RawQuery

	if strings.Contains(r.URL.Path, "streamGenerateContent") {
		p.serveStreamHTTP(w, r, started, upstreamURL, body)
		return
	}

	req, err := newUpstreamRequest(r, upstreamURL, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		p.writeCapture(newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, 0, nil, nil, err))
		return
	}

	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		p.writeCapture(newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, 0, nil, nil, err))
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		_, _ = w.Write(responseBody)
	}
	p.writeCapture(newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, resp.StatusCode, resp.Header, responseBody, readErr))
}

func (p *CloudStreamProxy) serveStreamHTTP(w http.ResponseWriter, r *http.Request, started time.Time, upstreamURL url.URL, body []byte) {
	attempts := p.streamRetryAttempts
	if attempts < 1 {
		attempts = 1
	}

	var resp *http.Response
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		req, reqErr := newUpstreamRequest(r, upstreamURL, body)
		if reqErr != nil {
			http.Error(w, reqErr.Error(), http.StatusBadGateway)
			p.writeCapture(newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, 0, nil, nil, reqErr))
			return
		}

		resp, err = p.client.Do(req)
		if err != nil {
			rec := newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, 0, nil, nil, err)
			rec.RetryAttempt = attempt
			rec.Retryable = attempt < attempts
			p.writeCapture(rec)
			if attempt < attempts {
				if !sleepWithContext(r.Context(), p.retryDelay(nil, nil, attempt)) {
					return
				}
				continue
			}
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		if shouldRetryStreamStatus(resp.StatusCode) {
			responseBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			rec := newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, resp.StatusCode, resp.Header, responseBody, readErr)
			rec.RetryAttempt = attempt
			rec.Retryable = attempt < attempts
			if attempt < attempts && readErr == nil {
				delay := p.retryDelay(resp.Header, responseBody, attempt)
				rec.RetryDelayMillis = delay.Milliseconds()
				p.writeCapture(rec)
				if !sleepWithContext(r.Context(), delay) {
					return
				}
				continue
			}
			p.writeCapture(rec)
			copyResponseHeaders(w.Header(), resp.Header)
			w.WriteHeader(resp.StatusCode)
			if readErr == nil {
				_, _ = w.Write(responseBody)
			}
			return
		}
		break
	}
	if resp == nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			_, _ = w.Write(responseBody)
		}
		p.writeCapture(newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, resp.StatusCode, resp.Header, responseBody, readErr))
		return
	}

	sub := p.claimSubscription(body)
	defer func() {
		if sub != nil {
			sub.close()
			p.removeSub(sub)
		}
	}()

	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReader(resp.Body)
	var data bytes.Buffer
	var responseBody bytes.Buffer
	var streamErr error
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			responseBody.Write(line)
			if _, err := w.Write(line); err != nil {
				p.writeCapture(newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, resp.StatusCode, resp.Header, responseBody.Bytes(), err))
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			processSSELine(line, &data, sub)
		}
		if readErr != nil {
			if readErr != io.EOF {
				fmt.Printf("CloudCode stream proxy read error: %v\n", readErr)
				streamErr = readErr
			}
			p.writeCapture(newCloudCaptureRecord(p.nextCaptureSeq(), started, r, body, resp.StatusCode, resp.Header, responseBody.Bytes(), streamErr))
			return
		}
	}
}

func newUpstreamRequest(r *http.Request, upstreamURL url.URL, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyRequestHeaders(req.Header, r.Header)
	return req, nil
}

func (p *CloudStreamProxy) nextCaptureSeq() uint64 {
	return atomic.AddUint64(&p.captureSeq, 1)
}

type cloudCaptureRecord struct {
	Seq                uint64              `json:"seq"`
	StartedAt          string              `json:"started_at"`
	DurationMillis     int64               `json:"duration_millis"`
	RetryAttempt       int                 `json:"retry_attempt,omitempty"`
	Retryable          bool                `json:"retryable,omitempty"`
	RetryDelayMillis   int64               `json:"retry_delay_millis,omitempty"`
	Method             string              `json:"method"`
	Path               string              `json:"path"`
	Query              string              `json:"query,omitempty"`
	RequestHeaders     map[string][]string `json:"request_headers,omitempty"`
	RequestBody        string              `json:"request_body"`
	RequestBodySHA256  string              `json:"request_body_sha256"`
	ResponseStatus     int                 `json:"response_status"`
	ResponseHeaders    map[string][]string `json:"response_headers,omitempty"`
	ResponseBody       string              `json:"response_body"`
	ResponseBodySHA256 string              `json:"response_body_sha256"`
	ResponseSSEEvents  int                 `json:"response_sse_events,omitempty"`
	Error              string              `json:"error,omitempty"`
}

func newCloudCaptureRecord(seq uint64, started time.Time, r *http.Request, reqBody []byte, status int, respHeader http.Header, respBody []byte, err error) cloudCaptureRecord {
	rec := cloudCaptureRecord{
		Seq:                seq,
		StartedAt:          started.UTC().Format(time.RFC3339Nano),
		DurationMillis:     time.Since(started).Milliseconds(),
		Method:             r.Method,
		Path:               r.URL.Path,
		Query:              r.URL.RawQuery,
		RequestHeaders:     sanitizeHeaders(r.Header),
		RequestBody:        string(reqBody),
		RequestBodySHA256:  sha256Hex(reqBody),
		ResponseStatus:     status,
		ResponseHeaders:    sanitizeHeaders(respHeader),
		ResponseBody:       string(respBody),
		ResponseBodySHA256: sha256Hex(respBody),
		ResponseSSEEvents:  countSSEEvents(respBody),
	}
	if err != nil {
		rec.Error = err.Error()
	}
	return rec
}

func (p *CloudStreamProxy) writeCapture(rec cloudCaptureRecord) {
	if p.capturePath == "" {
		return
	}
	p.captureMu.Lock()
	defer p.captureMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(p.capturePath), 0o700); err != nil {
		fmt.Printf("CloudCode stream capture mkdir failed: %v\n", err)
		return
	}
	f, err := os.OpenFile(p.capturePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Printf("CloudCode stream capture open failed: %v\n", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(rec); err != nil {
		fmt.Printf("CloudCode stream capture write failed: %v\n", err)
	}
}

func sanitizeHeaders(src http.Header) map[string][]string {
	if src == nil {
		return nil
	}
	dst := make(map[string][]string, len(src))
	for key, values := range src {
		lower := strings.ToLower(key)
		redact := lower == "authorization" ||
			lower == "proxy-authorization" ||
			lower == "x-goog-api-key" ||
			lower == "x-api-key" ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "credential")
		copied := make([]string, 0, len(values))
		for _, value := range values {
			if redact {
				copied = append(copied, "<redacted>")
			} else {
				copied = append(copied, value)
			}
		}
		dst[key] = copied
	}
	return dst
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func countSSEEvents(body []byte) int {
	count := 0
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		if bytes.HasPrefix(bytes.TrimSpace(scanner.Bytes()), []byte("data:")) {
			count++
		}
	}
	return count
}

func shouldRetryStreamStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

func (p *CloudStreamProxy) retryDelay(header http.Header, body []byte, attempt int) time.Duration {
	if header != nil {
		if delay := parseRetryAfter(header.Get("Retry-After")); delay > 0 {
			return clampDuration(delay, p.streamRetryBase, p.streamRetryMax)
		}
	}
	if delay := parseQuotaResetDelay(body); delay > 0 {
		return clampDuration(delay, p.streamRetryBase, p.streamRetryMax)
	}
	delay := p.streamRetryBase
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	return clampDuration(delay, p.streamRetryBase, p.streamRetryMax)
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return time.Until(when)
}

func parseQuotaResetDelay(body []byte) time.Duration {
	if len(body) == 0 {
		return 0
	}
	var payload struct {
		Error struct {
			Details []struct {
				Metadata map[string]string `json:"metadata"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	for _, detail := range payload.Error.Details {
		for key, value := range detail.Metadata {
			if key != "quotaResetDelay" {
				continue
			}
			delay, err := time.ParseDuration(value)
			if err == nil {
				return delay
			}
		}
	}
	return 0
}

func clampDuration(value time.Duration, minimum time.Duration, maximum time.Duration) time.Duration {
	if minimum > 0 && value < minimum {
		return minimum
	}
	if maximum > 0 && value > maximum {
		return maximum
	}
	return value
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func envInt(key string, fallback int, min int, max int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < min {
		return min
	}
	if parsed > max {
		return max
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err == nil {
		return parsed
	}
	if millis, err := strconv.Atoi(value); err == nil {
		return time.Duration(millis) * time.Millisecond
	}
	return fallback
}

func (p *CloudStreamProxy) claimSubscription(body []byte) *cloudStreamSubscription {
	p.mu.Lock()
	defer p.mu.Unlock()

	firstActiveIndex := -1
	for i, sub := range p.subs {
		if sub.ctx.Err() != nil {
			continue
		}
		if firstActiveIndex == -1 {
			firstActiveIndex = i
		}
		if sub.prompt == "" || bytes.Contains(body, []byte(sub.prompt)) {
			p.subs = append(p.subs[:i], p.subs[i+1:]...)
			fmt.Printf("CloudCode stream tap claimed request prompt_bytes=%d body_bytes=%d\n", len(sub.prompt), len(body))
			return sub
		}
	}
	if firstActiveIndex >= 0 {
		sub := p.subs[firstActiveIndex]
		p.subs = append(p.subs[:firstActiveIndex], p.subs[firstActiveIndex+1:]...)
		fmt.Printf("CloudCode stream tap claimed request by fallback prompt_bytes=%d body_bytes=%d\n", len(sub.prompt), len(body))
		return sub
	}
	fmt.Printf("CloudCode stream tap had no subscriber body_bytes=%d\n", len(body))
	return nil
}

func (p *CloudStreamProxy) removeSub(sub *cloudStreamSubscription) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, candidate := range p.subs {
		if candidate == sub {
			p.subs = append(p.subs[:i], p.subs[i+1:]...)
			return
		}
	}
}

func (s *cloudStreamSubscription) send(chunk *interfaces.StreamChunk) {
	if s == nil || chunk == nil {
		return
	}
	select {
	case <-s.ctx.Done():
	case s.ch <- chunk:
	}
}

func (s *cloudStreamSubscription) close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.cancel()
		close(s.ch)
	})
}

func processSSELine(line []byte, data *bytes.Buffer, sub *cloudStreamSubscription) {
	trimmed := bytes.TrimRight(line, "\r\n")
	if len(trimmed) == 0 {
		if data.Len() > 0 {
			emitCloudStreamEvent(bytes.TrimSpace(data.Bytes()), sub)
			data.Reset()
		}
		return
	}
	trimmed = bytes.TrimSpace(trimmed)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	if bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	if data.Len() > 0 {
		data.WriteByte('\n')
	}
	data.Write(payload)
}

func emitCloudStreamEvent(payload []byte, sub *cloudStreamSubscription) {
	if sub == nil || len(payload) == 0 {
		return
	}

	var event struct {
		Response struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			UsageMetadata *struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		} `json:"response"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}

	var usage *models.UsageReport
	if event.Response.UsageMetadata != nil {
		usage = &models.UsageReport{
			PromptTokens:     event.Response.UsageMetadata.PromptTokenCount,
			CompletionTokens: event.Response.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      event.Response.UsageMetadata.TotalTokenCount,
		}
	}
	for _, candidate := range event.Response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Thought || part.Text == "" {
				continue
			}
			sub.send(&interfaces.StreamChunk{Content: part.Text, Usage: usage})
			usage = nil
		}
	}
	if usage != nil {
		sub.send(&interfaces.StreamChunk{Usage: usage})
	}
}

func copyRequestHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "host", "content-length", "accept-encoding", "connection", "transfer-encoding":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "transfer-encoding", "connection":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func joinURLPath(basePath string, requestPath string) string {
	if basePath == "" || basePath == "/" {
		return requestPath
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(requestPath, "/")
}
