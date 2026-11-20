package config

import (
	"errors"
	"github.com/joho/godotenv"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr, DatabaseURL, JWTSecret, RazorpayKeyID, RazorpayKeySecret, RazorpayWebhookSecret, SMTPHost, SMTPUsername, SMTPPassword, SMTPFrom, QuaiRPCURL, QuaiContractAddress, QuaiPrivateKey, DeviceAPIKeyPepper string
	AccessTTL, RefreshTTL                                                                                                                                                                                          time.Duration
	QuaiChainID                                                                                                                                                                                                    int64
	RateLimitRPS                                                                                                                                                                                                   float64
	MaxTickets                                                                                                                                                                                                     int
}

func Load() (Config, error) {
	_ = godotenv.Load()
	c := Config{HTTPAddr: env("HTTP_ADDR", ":8080"), DatabaseURL: os.Getenv("DATABASE_URL"), JWTSecret: os.Getenv("JWT_SECRET"), RazorpayKeyID: os.Getenv("RAZORPAY_KEY_ID"), RazorpayKeySecret: os.Getenv("RAZORPAY_KEY_SECRET"), RazorpayWebhookSecret: os.Getenv("RAZORPAY_WEBHOOK_SECRET"), SMTPHost: os.Getenv("SMTP_HOST"), SMTPUsername: os.Getenv("SMTP_USERNAME"), SMTPPassword: os.Getenv("SMTP_PASSWORD"), SMTPFrom: os.Getenv("SMTP_FROM"), QuaiRPCURL: env("QUAI_RPC_URL", "https://orchard.rpc.quai.network/cyprus1"), QuaiContractAddress: os.Getenv("QUAI_CONTRACT_ADDRESS"), QuaiPrivateKey: os.Getenv("QUAI_PRIVATE_KEY"), DeviceAPIKeyPepper: os.Getenv("DEVICE_API_KEY_PEPPER"), AccessTTL: duration("ACCESS_TOKEN_TTL", 15*time.Minute), RefreshTTL: duration("REFRESH_TOKEN_TTL", 30*24*time.Hour), QuaiChainID: int64(integer("QUAI_CHAIN_ID", 15000)), RateLimitRPS: decimal("RATE_LIMIT_RPS", 10), MaxTickets: integer("MAX_TICKETS_PER_BOOKING", 8)}
	if c.DatabaseURL == "" || len(c.JWTSecret) < 32 {
		return c, errors.New("DATABASE_URL and JWT_SECRET (at least 32 characters) are required")
	}
	if len(c.DeviceAPIKeyPepper) < 32 {
		return c, errors.New("DEVICE_API_KEY_PEPPER (at least 32 characters) is required")
	}
	if (c.RazorpayKeyID != "" || c.RazorpayKeySecret != "" || c.RazorpayWebhookSecret != "") && (c.RazorpayKeyID == "" || c.RazorpayKeySecret == "" || c.RazorpayWebhookSecret == "") {
		return c, errors.New("all three Razorpay credentials must be configured together")
	}
	return c, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func duration(k string, d time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if x, e := time.ParseDuration(v); e == nil {
			return x
		}
	}
	return d
}
func integer(k string, d int) int {
	if v, e := strconv.Atoi(os.Getenv(k)); e == nil && v > 0 {
		return v
	}
	return d
}
func decimal(k string, d float64) float64 {
	if v, e := strconv.ParseFloat(os.Getenv(k), 64); e == nil && v > 0 {
		return v
	}
	return d
}
