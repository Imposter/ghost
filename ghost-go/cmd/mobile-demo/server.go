package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Imposter/ghost/ghost-go/mobile"
)

// HTTPTestServer provides HTTP endpoints for testing tunnel connectivity.
type HTTPTestServer struct {
	server   *http.Server
	listener net.Listener
	logger   *slog.Logger
}

// NewHTTPTestServer creates a new HTTP test server.
func NewHTTPTestServer(listener net.Listener, logger *slog.Logger) *HTTPTestServer {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	s := &HTTPTestServer{
		listener: listener,
		logger:   logger,
	}

	// Register endpoints
	mux.HandleFunc("/test", s.handleTest)
	mux.HandleFunc("/echo", s.handleEcho)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/info", s.handleInfo)

	s.server = &http.Server{
		Handler: mux,
	}

	return s
}

// Start starts the HTTP server.
func (s *HTTPTestServer) Start() error {
	s.logger.Info("Starting HTTP test server", "addr", s.listener.Addr().String())
	return s.server.Serve(s.listener)
}

// Close stops the HTTP server.
func (s *HTTPTestServer) Close() error {
	s.logger.Info("Stopping HTTP test server")
	return s.server.Close()
}

// handleTest returns a JSON test response.
func (s *HTTPTestServer) handleTest(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("Test endpoint hit", "method", r.Method, "remote", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")

	response := map[string]interface{}{
		"message":   "Hello from desktop server",
		"timestamp": time.Now().Unix(),
		"method":    r.Method,
		"path":      r.URL.Path,
		"query":     r.URL.RawQuery,
		"peerIP":    mobile.DesktopVirtualIP,
	}

	json.NewEncoder(w).Encode(response)
}

// handleEcho echoes the request body.
func (s *HTTPTestServer) handleEcho(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("Echo endpoint hit", "method", r.Method, "remote", r.RemoteAddr)

	// Copy Content-Type header
	contentType := r.Header.Get("Content-Type")
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	// Read and echo body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	// Add echo header
	w.Header().Set("X-Echo-Length", fmt.Sprintf("%d", len(body)))
	w.Write(body)
}

// handleHealth returns a simple health check response.
func (s *HTTPTestServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleInfo returns detailed server information.
func (s *HTTPTestServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("Info endpoint hit", "method", r.Method, "remote", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")

	// Collect request headers
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	response := map[string]interface{}{
		"server": map[string]interface{}{
			"name":      "ghost-go mobile-demo",
			"version":   "1.0.0",
			"virtualIP": mobile.DesktopVirtualIP,
			"port":      mobile.HTTPTestPort,
		},
		"request": map[string]interface{}{
			"method":     r.Method,
			"path":       r.URL.Path,
			"query":      r.URL.RawQuery,
			"remoteAddr": r.RemoteAddr,
			"headers":    headers,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	}

	json.NewEncoder(w).Encode(response)
}
