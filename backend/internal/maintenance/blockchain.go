package maintenance

import (
	"context"
	"log"
	"time"

	"github.com/fairpass/backend/internal/config"
	"github.com/fairpass/backend/internal/quai"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func processBlockchainJobs(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) {
	if cfg.QuaiBridgeToken == "" || cfg.QuaiContractAddress == "" {
		return
	}
	client := quai.Client{BaseURL: cfg.QuaiBridgeURL, Token: cfg.QuaiBridgeToken}
	// Poll receipts already recorded before submitting more queued writes.
	rows, err := pool.Query(ctx, `SELECT id,tx_hash,ticket_id FROM blockchain_transactions WHERE status='submitted' AND tx_hash IS NOT NULL ORDER BY created_at LIMIT 20`)
	if err == nil {
		type pending struct{ id, hash, ticketID string }
		list := []pending{}
		for rows.Next() {
			var p pending
			if rows.Scan(&p.id, &p.hash, &p.ticketID) == nil {
				list = append(list, p)
			}
		}
		rows.Close()
		for _, p := range list {
			jobCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			result, e := client.Status(jobCtx, p.hash, cfg.QuaiConfirmations)
			cancel()
			if e != nil {
				continue
			}
			if result.Status != "confirmed" && result.Status != "failed" {
				continue
			}
			state := result.Status
			_, e = pool.Exec(ctx, `UPDATE blockchain_transactions SET status=$2,last_error=CASE WHEN $2='failed' THEN 'transaction reverted on Quai' ELSE NULL END,updated_at=now() WHERE id=$1 AND status='submitted'`, p.id, state)
			if e == nil && state == "confirmed" {
				_, e = pool.Exec(ctx, `UPDATE tickets SET onchain_id=ticket_hash WHERE id=$1`, p.ticketID)
			}
			if e != nil {
				log.Printf("Quai transaction status update failed: %v", e)
			}
		}
	}
	for i := 0; i < 5; i++ {
		jobCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		tx, e := pool.Begin(jobCtx)
		if e != nil {
			cancel()
			return
		}
		var jobID, operation, ticketHash, target string
		e = tx.QueryRow(jobCtx, `SELECT bt.id,bt.operation,t.ticket_hash,COALESCE(bt.target_address,'')
   FROM blockchain_transactions bt JOIN tickets t ON t.id=bt.ticket_id
   WHERE bt.status='queued' AND bt.updated_at<now()-interval '15 seconds'
    AND (bt.operation='redeem' OR bt.target_address IS NOT NULL)
   ORDER BY bt.created_at FOR UPDATE OF bt SKIP LOCKED LIMIT 1`).Scan(&jobID, &operation, &ticketHash, &target)
		if e != nil {
			_ = tx.Rollback(jobCtx)
			cancel()
			if e != pgx.ErrNoRows {
				log.Printf("Quai job query failed: %v", e)
			}
			return
		}
		id := "0x" + ticketHash
		result, callErr := client.Submit(jobCtx, operation, id, target)
		if callErr != nil {
			var attempts int
			_ = tx.QueryRow(jobCtx, `UPDATE blockchain_transactions SET attempts=attempts+1,last_error=$2,updated_at=now() WHERE id=$1 RETURNING attempts`, jobID, clipError(callErr)).Scan(&attempts)
			if attempts >= 20 {
				_, _ = tx.Exec(jobCtx, `UPDATE blockchain_transactions SET status='failed' WHERE id=$1`, jobID)
			}
			if e = tx.Commit(jobCtx); e != nil {
				log.Printf("Quai retry update failed: %v", e)
			}
			cancel()
			continue
		}
		if result.Status == "confirmed" {
			_, e = tx.Exec(jobCtx, `UPDATE blockchain_transactions SET status='confirmed',tx_hash=NULLIF($2,''),attempts=attempts+1,last_error=NULL,updated_at=now() WHERE id=$1`, jobID, result.TxHash)
			if e == nil {
				_, e = tx.Exec(jobCtx, `UPDATE tickets SET onchain_id=ticket_hash WHERE id=(SELECT ticket_id FROM blockchain_transactions WHERE id=$1)`, jobID)
			}
		} else {
			_, e = tx.Exec(jobCtx, `UPDATE blockchain_transactions SET status='submitted',tx_hash=$2,attempts=attempts+1,last_error=NULL,updated_at=now() WHERE id=$1`, jobID, result.TxHash)
		}
		if e == nil {
			e = tx.Commit(jobCtx)
		} else {
			_ = tx.Rollback(jobCtx)
		}
		cancel()
		if e != nil {
			log.Printf("Quai job result save failed: %v", e)
		}
	}
}

func clipError(err error) string {
	msg := err.Error()
	if len(msg) > 500 {
		return msg[:500]
	}
	return msg
}
