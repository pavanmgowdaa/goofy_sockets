package maintenance

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/fairpass/backend/internal/config"
	"github.com/fairpass/backend/internal/payments"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run performs short bounded cleanup jobs and exits when the process context ends.
func Run(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			if err := expireReservations(jobCtx, pool); err != nil {
				log.Printf("reservation cleanup failed: %v", err)
			}
			if _, err := pool.Exec(jobCtx, `DELETE FROM rate_limit_buckets WHERE updated_at<now()-interval '2 hours'`); err != nil {
				log.Printf("rate limit cleanup failed: %v", err)
			}
			if _, err := pool.Exec(jobCtx, `DELETE FROM login_throttles WHERE updated_at<now()-interval '30 days'`); err != nil {
				log.Printf("login throttle cleanup failed: %v", err)
			}
			if _, err := pool.Exec(jobCtx, `DELETE FROM idempotency_keys WHERE expires_at<now()`); err != nil {
				log.Printf("idempotency cleanup failed: %v", err)
			}
			if _, err := pool.Exec(jobCtx, `DELETE FROM refresh_tokens WHERE expires_at<now() OR revoked_at<now()-interval '30 days'`); err != nil {
				log.Printf("refresh token cleanup failed: %v", err)
			}
			if cfg.RazorpayKeyID != "" && cfg.RazorpayKeySecret != "" {
				processRefundJobs(jobCtx, pool, payments.Razorpay{KeyID: cfg.RazorpayKeyID, Secret: cfg.RazorpayKeySecret, BaseURL: cfg.RazorpayBaseURL})
			}
			processBlockchainJobs(jobCtx, pool, cfg)
			cancel()
		}
	}
}

func expireReservations(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `WITH expired AS (
  UPDATE bookings SET status='cancelled',updated_at=now()
  WHERE status='pending' AND expires_at IS NOT NULL AND expires_at<=now()
  RETURNING id
 ), quantities AS (
  SELECT bi.ticket_type_id,SUM(bi.quantity)::integer AS quantity
  FROM booking_items bi JOIN expired e ON e.id=bi.booking_id
  GROUP BY bi.ticket_type_id
 )
 UPDATE ticket_types tt SET quantity_reserved=GREATEST(0,tt.quantity_reserved-q.quantity)
 FROM quantities q WHERE tt.id=q.ticket_type_id`)
	return err
}

func processRefundJobs(ctx context.Context, pool *pgxpool.Pool, client payments.Razorpay) {
	for i := 0; i < 5; i++ {
		jobCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		tx, err := pool.Begin(jobCtx)
		if err != nil {
			cancel()
			log.Printf("refund job claim failed: %v", err)
			return
		}
		var refundID, paymentID, primaryPaymentID string
		var amount int64
		var attempts int
		err = tx.QueryRow(jobCtx, `SELECT r.id,COALESCE(r.provider_payment_id,p.provider_payment_id,''),COALESCE(p.provider_payment_id,''),r.amount_minor,r.attempts
   FROM refunds r JOIN payments p ON p.id=r.payment_id
   WHERE (r.status IN ('requires_refund','retry') AND r.next_attempt_at<=now())
      OR (r.status='processing' AND r.updated_at<now()-interval '5 minutes')
   ORDER BY r.created_at FOR UPDATE OF r SKIP LOCKED LIMIT 1`).Scan(&refundID, &paymentID, &primaryPaymentID, &amount, &attempts)
		if err != nil {
			_ = tx.Rollback(jobCtx)
			cancel()
			if err != pgx.ErrNoRows {
				log.Printf("refund job claim failed: %v", err)
			}
			return
		}
		_, err = tx.Exec(jobCtx, `UPDATE refunds SET status='processing',attempts=attempts+1,updated_at=now() WHERE id=$1`, refundID)
		if err == nil {
			err = tx.Commit(jobCtx)
		} else {
			_ = tx.Rollback(jobCtx)
		}
		cancel()
		if err != nil {
			log.Printf("refund job claim failed: %v", err)
			return
		}
		reqCtx, reqCancel := context.WithTimeout(ctx, 20*time.Second)
		result, callErr := client.CreateRefund(reqCtx, paymentID, amount, refundID, "fairpass-"+refundID)
		reqCancel()
		if callErr != nil {
			retry := true
			var apiErr *payments.APIError
			if errors.As(callErr, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != 409 {
				retry = false
			}
			state := "failed"
			delay := time.Duration(0)
			if retry && attempts+1 < 8 {
				state = "retry"
				delay = time.Minute * time.Duration(1<<min(attempts, 6))
			}
			errMsg := callErr.Error()
			if len(errMsg) > 500 {
				errMsg = errMsg[:500]
			}
			_, err = pool.Exec(ctx, `UPDATE refunds SET status=$2,last_error=$3,next_attempt_at=now()+$4::interval,updated_at=now() WHERE id=$1`, refundID, state, errMsg, delay.String())
			if err != nil {
				log.Printf("refund retry update failed: %v", err)
			}
			continue
		}
		state := result.Status
		if state != "processed" && state != "failed" {
			state = "pending"
		}
		finishCtx, finishCancel := context.WithTimeout(ctx, 10*time.Second)
		tx, err = pool.Begin(finishCtx)
		if err == nil {
			_, err = tx.Exec(finishCtx, `UPDATE refunds SET provider_refund_id=$2,status=$3,last_error=NULL,updated_at=now() WHERE id=$1`, refundID, result.ID, state)
			if err == nil && state == "processed" {
				err = markRefundCompleted(finishCtx, tx, refundID, paymentID, primaryPaymentID)
			}
			if err == nil {
				err = tx.Commit(finishCtx)
			} else {
				_ = tx.Rollback(finishCtx)
			}
		}
		finishCancel()
		if err != nil {
			log.Printf("refund result update failed: %v", err)
		}
	}
}

func markRefundCompleted(ctx context.Context, tx pgx.Tx, refundID, paymentID, primaryPaymentID string) error {
	if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status='refunded',updated_at=now() WHERE provider_payment_id=$1`, paymentID); err != nil {
		return err
	}
	if paymentID == primaryPaymentID {
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='refunded',updated_at=now() WHERE provider_payment_id=$1`, paymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings b SET status='refunded',updated_at=now() FROM refunds r WHERE r.id=$1 AND b.id=(SELECT booking_id FROM payments WHERE id=r.payment_id) AND b.status='paid'`, refundID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE tickets SET status='refunded' WHERE booking_id=(SELECT p.booking_id FROM payments p JOIN refunds r ON r.payment_id=p.id WHERE r.id=$1) AND status IN ('active','transferred')`, refundID)
		return err
	}
	return nil
}
