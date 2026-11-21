package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fairpass/internal/auth"
	"fairpass/internal/database"
	"fairpass/internal/integration"
	"fairpass/internal/sync"
	"fairpass/internal/validation"
)

type APIResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Version   string `json:"version,omitempty"`
	Protocol  string `json:"protocol"`
	Timestamp string `json:"timestamp"`
	Database  string `json:"database,omitempty"`
}

func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Response encoding error: %v", err)
	}
}

func healthHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbStatus := "disconnected"
		if db != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := db.Ping(ctx); err == nil {
				dbStatus = "connected"
			}
		}

		jsonResponse(w, http.StatusOK, APIResponse{
			Status:    "healthy",
			Service:   "FAIRPASS Backend",
			Protocol:  "HTTPS",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Database:  dbStatus,
		})
	}
}

func apiStatusHandler(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, APIResponse{
		Status:    "operational",
		Service:   "FAIRPASS API",
		Version:   "v1",
		Protocol:  "HTTPS",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// scanHandler handles incoming ticket scans from gate devices.
func scanHandler(validator *validation.Validator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if validator == nil {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]string{
				"error": "database is unavailable",
			})
			return
		}

		var req database.ScanRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{
				"error": "invalid scan request JSON: " + err.Error(),
			})
			return
		}

		if (req.TicketID == "" && req.QRToken == "") || req.DeviceIdentifier == "" {
			jsonResponse(w, http.StatusBadRequest, map[string]string{
				"error": "qr_token (or legacy ticket_id) and device_identifier are required",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		resp, err := validator.Validate(ctx, req)
		if err != nil {
			log.Printf("Scan validation error: %v", err)
			jsonResponse(w, http.StatusInternalServerError, map[string]string{
				"error": "internal verification failure: " + err.Error(),
			})
			return
		}

		// Return HTTP 200 with clear scan result (VALID, DUPLICATE, WRONG_GATE, etc.)
		status := http.StatusOK
		if !resp.Accepted {
			// All rejections return 200 or 400 depending on client contract;
			// returning 200 with Accepted=false preserves detailed ScanResult.
			status = http.StatusOK
		}

		jsonResponse(w, status, resp)
	}
}

// syncOutboxHandler allows manually triggering outbox synchronization.
func syncOutboxHandler(syncService *sync.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if syncService == nil || !syncService.IsUpstreamAvailable() {
			jsonResponse(w, http.StatusOK, map[string]any{
				"status":    "offline",
				"message":   "global synchronization is unavailable",
				"processed": 0,
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		count, err := syncService.ProcessOutbox(ctx, 50)
		if err != nil {
			jsonResponse(w, http.StatusOK, map[string]any{
				"status":    "offline",
				"message":   "operating in offline mode; outbox queued locally",
				"processed": count,
				"error":     err.Error(),
			})
			return
		}

		jsonResponse(w, http.StatusOK, map[string]any{
			"status":    "synced",
			"processed": count,
		})
	}
}

// syncManifestHandler receives an authoritative manifest package and ingests it locally.
func syncManifestHandler(syncService *sync.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if syncService == nil {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]string{
				"error": "sync service unavailable",
			})
			return
		}

		var pkg sync.ManifestPackage
		if err := json.NewDecoder(r.Body).Decode(&pkg); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{
				"error": "invalid manifest payload: " + err.Error(),
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		if err := syncService.IngestManifest(ctx, &pkg); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{
				"error": "failed to ingest manifest: " + err.Error(),
			})
			return
		}

		jsonResponse(w, http.StatusOK, map[string]any{
			"status":   "manifest_ingested",
			"event_id": pkg.Event.ID,
			"tickets":  len(pkg.Tickets),
			"gates":    len(pkg.Gates),
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")

		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		log.Printf(
			"%s %s %s",
			r.Method,
			r.URL.Path,
			time.Since(start),
		)
	})
}

func main() {
	addr := os.Getenv("FAIRPASS_ADDR")
	if addr == "" {
		addr = ":8443"
	}

	certFile := os.Getenv("FAIRPASS_TLS_CERT")
	if certFile == "" {
		certFile = "certs/server-cert.pem"
	}

	keyFile := os.Getenv("FAIRPASS_TLS_KEY")
	if keyFile == "" {
		keyFile = "certs/server-key.pem"
	}

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	// Initialize Database Connection Pool
	dbConfig := database.LoadConfigFromEnv()
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	db, err := database.NewConnectionPool(dbCtx, dbConfig)
	dbCancel()

	var validator *validation.Validator
	var syncService *sync.Service

	if err != nil {
		log.Printf("Warning: Database connection failed during startup: %v", err)
		log.Println("Server continuing in offline/unconnected mode...")
	} else {
		defer db.Close()
		authenticator := auth.NewDeviceAuthenticator(db)
		secretKey := os.Getenv("FAIRPASS_SECRET_KEY")
		validator = validation.NewValidator(db, authenticator, secretKey)

		var upstream sync.UpstreamClient
		if globalDSN := os.Getenv("FAIRPASS_GLOBAL_DATABASE_URL"); globalDSN != "" {
			globalCtx, globalCancel := context.WithTimeout(context.Background(), 5*time.Second)
			globalClient, globalErr := integration.OpenGlobalPostgres(globalCtx, globalDSN)
			globalCancel()
			if globalErr != nil {
				log.Printf("Warning: global database sync is unavailable: %v", globalErr)
			} else {
				defer globalClient.Close()
				upstream = globalClient
				log.Println("Global FAIRPASS database adapter connected")
			}
		}
		syncService = sync.NewSyncService(db, upstream, secretKey)

		// Start background synchronization worker
		syncService.StartBackgroundSync(serverCtx, 10*time.Second)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", healthHandler(db))
	mux.HandleFunc("GET /api/v1/status", apiStatusHandler)
	mux.HandleFunc("POST /api/v1/scan", scanHandler(validator))
	mux.HandleFunc("POST /api/v1/sync/outbox", syncOutboxHandler(syncService))
	mux.HandleFunc("POST /api/v1/sync/manifest", syncManifestHandler(syncService))

	handler := securityHeaders(requestLogger(mux))

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("FAIRPASS HTTPS server starting on %s", addr)
		log.Printf("Health endpoint: https://localhost%s/health", addr)
		log.Printf("API endpoint: https://localhost%s/api/v1/status", addr)
		log.Printf("Scan endpoint: https://localhost%s/api/v1/scan", addr)

		// Start TLS if cert files exist, otherwise fallback to standard HTTP for dev
		if _, err := os.Stat(certFile); err == nil {
			err = server.ListenAndServeTLS(certFile, keyFile)
		} else {
			log.Printf("TLS cert not found at '%s', starting standard HTTP on %s", certFile, addr)
			err = server.ListenAndServe()
		}

		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-stop

	log.Println("Shutting down FAIRPASS server...")
	serverCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Graceful shutdown failed: %v", err)
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("Forced shutdown failed: %v", closeErr)
		}
	}

	log.Println("FAIRPASS server stopped")
}
