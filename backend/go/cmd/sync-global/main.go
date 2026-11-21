package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"fairpass/internal/database"
	"fairpass/internal/integration"
	fairsync "fairpass/internal/sync"
)

func main() {
	eventID := os.Getenv("FAIRPASS_EVENT_ID")
	if eventID == "" {
		log.Fatal("FAIRPASS_EVENT_ID is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	localDB, err := database.NewConnectionPool(ctx, database.LoadConfigFromEnv())
	if err != nil {
		log.Fatalf("connect local venue database: %v", err)
	}
	defer localDB.Close()

	global, err := integration.OpenGlobalPostgres(ctx, os.Getenv("FAIRPASS_GLOBAL_DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect global database: %v", err)
	}
	defer global.Close()

	service := fairsync.NewSyncService(localDB, global, os.Getenv("FAIRPASS_SECRET_KEY"))
	pkg, err := global.FetchManifest(ctx, eventID, 0)
	if err != nil {
		log.Fatalf("fetch global event manifest: %v", err)
	}
	if err := service.IngestManifest(ctx, pkg); err != nil {
		log.Fatalf("ingest local manifest: %v", err)
	}
	fmt.Printf("Manifest imported: event=%s tickets=%d gates=%d\n", pkg.Event.ID, len(pkg.Tickets), len(pkg.Gates))

	revocations, err := global.FetchRevocations(ctx, eventID, 0)
	if err != nil {
		log.Fatalf("fetch global revocations: %v", err)
	}
	if err := service.IngestRevocations(ctx, revocations); err != nil {
		log.Fatalf("ingest local revocations: %v", err)
	}
	fmt.Printf("Revocations imported: %d\n", len(revocations))

	count, err := service.ProcessOutbox(ctx, 100)
	if err != nil {
		log.Printf("Scan upload deferred (venue remains offline-capable): %v", err)
	} else {
		fmt.Printf("Local scan records uploaded: %d\n", count)
	}
}
