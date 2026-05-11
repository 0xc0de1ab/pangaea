package extserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	subscribePath             = "/exa.extension_server_pb.ExtensionServerService/SubscribeToUnifiedStateSyncTopic"
	pushUnifiedStatePath      = "/exa.extension_server_pb.ExtensionServerService/PushUnifiedStateSyncUpdate"
	languageServerStartedPath = "/exa.extension_server_pb.ExtensionServerService/LanguageServerStarted"
	heartbeatPath             = "/exa.extension_server_pb.ExtensionServerService/Heartbeat"
)

type Server struct {
	addr   string
	store  *StateStore
	server *http.Server
	ln     net.Listener
	mu     sync.Mutex
}

func New(addr, dbPath string) *Server {
	return &Server{
		addr:  addr,
		store: NewStateStore(dbPath),
	}
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc(subscribePath, s.handleSubscribe)
	mux.HandleFunc(pushUnifiedStatePath, s.handlePushUnifiedState)
	mux.HandleFunc(languageServerStartedPath, s.handleUnaryEmpty)
	mux.HandleFunc(heartbeatPath, s.handleUnaryEmpty)

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		fmt.Printf("ExtensionServerService listening on %s\n", ln.Addr().String())
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Printf("ExtensionServerService stopped unexpectedly: %v\n", err)
		}
	}()
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	server := s.server
	s.server = nil
	s.ln = nil
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload, err := readRequestPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	topic, ok := readStringField(payload, 1)
	if !ok || topic == "" {
		http.Error(w, "missing topic", http.StatusBadRequest)
		return
	}

	initialState, err := s.store.InitialState(topic)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Printf("ExtensionServerService: subscribed topic=%s initialStateBytes=%d\n", topic, len(initialState))
	update := encodeUnifiedStateInitialUpdate(initialState)
	if err := writeConnectFrame(w, update); err != nil {
		fmt.Printf("ExtensionServerService: failed to write initial state for %s: %v\n", topic, err)
		return
	}

	<-r.Context().Done()
}

func (s *Server) handlePushUnifiedState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload, err := readRequestPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.applyPushedUpdate(payload); err != nil {
		fmt.Printf("ExtensionServerService: received PushUnifiedStateSyncUpdate bytes=%d apply_error=%v\n", len(payload), err)
	} else {
		fmt.Printf("ExtensionServerService: received PushUnifiedStateSyncUpdate bytes=%d applied=true\n", len(payload))
	}
	writeUnaryProto(w, nil)
}

func (s *Server) applyPushedUpdate(payload []byte) error {
	update, ok := readBytesField(payload, 1)
	if !ok {
		return fmt.Errorf("missing update")
	}
	topic, ok := readStringField(update, 1)
	if !ok || topic == "" {
		return fmt.Errorf("missing update topic")
	}

	appliedUpdate, ok := readBytesField(update, 5)
	if !ok {
		return fmt.Errorf("missing applied_update for topic %s", topic)
	}
	rowKey, ok := readStringField(appliedUpdate, 1)
	if !ok || rowKey == "" {
		return fmt.Errorf("missing applied_update key for topic %s", topic)
	}
	newRow, _ := readBytesField(appliedUpdate, 2)
	deleted := readBoolField(appliedUpdate, 5)
	if !deleted && len(newRow) == 0 {
		return fmt.Errorf("missing applied_update new_row for topic %s key %s", topic, rowKey)
	}
	return s.store.ApplyRowUpdate(topic, rowKey, newRow, deleted)
}

func (s *Server) handleUnaryEmpty(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, _ = io.Copy(io.Discard, r.Body)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/connect+") {
		_ = writeConnectFrame(w, nil)
		return
	}
	writeUnaryProto(w, nil)
}

func writeUnaryProto(w http.ResponseWriter, payload []byte) {
	w.Header().Set("Content-Type", "application/proto")
	w.WriteHeader(http.StatusOK)
	if len(payload) > 0 {
		_, _ = w.Write(payload)
	}
}
