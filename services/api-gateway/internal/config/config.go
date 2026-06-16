// Package config 负责从环境变量加载 API 网关的运行配置，
// 并为缺省值提供解析辅助函数。缺失关键密钥（JWT_SECRET、API_KEY）时会直接终止启动。
package config

import (
	"log"
	"os"
	"strconv"
)

// Config holds the application configuration loaded from environment variables.
type Config struct {
	Port        string
	DatabaseURL string
	RedisURL    string
	LogLevel    string
	JWTSecret   string
	APIKey      string // accepted API key for development
	ShopID      string
	Domain      string
	RateLimit   RateLimitConfig
}

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	Enabled         bool
	Rate            float64 // requests per second
	Burst           int
	CleanupInterval int // seconds
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	// SECURITY FIX: F-003-05 — Removed hardcoded default JWT_SECRET and API_KEY.
	// If these are not set via environment variables, the service will fatal-exit
	// at startup rather than running with insecure defaults.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatalf("FATAL: JWT_SECRET environment variable is required but not set")
	}
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		log.Fatalf("FATAL: API_KEY environment variable is required but not set")
	}

	cfg := &Config{
		Port:        envOrDefault("API_PORT", "8080"),
		DatabaseURL: envOrDefault("DATABASE_URL", "postgres://ancf:ancf@localhost:5432/ancf?sslmode=disable"),
		RedisURL:    envOrDefault("REDIS_URL", "redis://localhost:6379/0"),
		LogLevel:    envOrDefault("LOG_LEVEL", "info"),
		JWTSecret:   jwtSecret,
		APIKey:      apiKey,
		ShopID:      envOrDefault("SHOP_ID", "zero_shop_sol_01"),
		Domain:      envOrDefault("SHOP_DOMAIN", "yourshop.com"),
		RateLimit: RateLimitConfig{
			Enabled:         envBoolOrDefault("RATE_LIMIT_ENABLED", true),
			Rate:            envFloatOrDefault("RATE_LIMIT_RATE", 10.0),
			Burst:           envIntOrDefault("RATE_LIMIT_BURST", 20),
			CleanupInterval: envIntOrDefault("RATE_LIMIT_CLEANUP_INTERVAL", 60),
		},
	}

	return cfg
}

// envOrDefault 返回环境变量 key 的值，未设置或为空时返回 defaultVal。
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// envBoolOrDefault 读取布尔型环境变量，未设置或解析失败时返回 defaultVal。
func envBoolOrDefault(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}

// envIntOrDefault 读取整型环境变量，未设置或解析失败时返回 defaultVal。
func envIntOrDefault(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return i
}

// envFloatOrDefault 读取浮点型环境变量，未设置或解析失败时返回 defaultVal。
func envFloatOrDefault(key string, defaultVal float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultVal
	}
	return f
}
