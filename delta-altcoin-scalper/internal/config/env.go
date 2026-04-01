package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadEnv reads environment variables and overrides DefaultSettings
func LoadEnv() Settings {
	cfg := DefaultSettings()

	// Delta config
	if v := os.Getenv("DELTA_API_KEY"); v != "" {
		cfg.Delta.APIKey = v
	}
	if v := os.Getenv("DELTA_API_SECRET"); v != "" {
		cfg.Delta.APISecret = v
	}
	if v := os.Getenv("DELTA_TESTNET"); v != "" {
		cfg.Delta.Testnet = strings.ToLower(v) == "true" || v == "1"
	}

	// Risk config
	if v := os.Getenv("INITIAL_CAPITAL"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Risk.InitialCapital = f
		}
	}
	if v := os.Getenv("RISK_PER_TRADE_PERCENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Risk.RiskPerTradePercent = f
		}
	}
	if v := os.Getenv("MAX_DAILY_LOSS_PERCENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Risk.MaxDailyLossPercent = f
		}
	}

	// DB config
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.DB.Host = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		cfg.DB.Port = v
	}
	if v := os.Getenv("DB_USER"); v != "" {
		cfg.DB.User = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.DB.Password = v
	}
	if v := os.Getenv("DB_NAME"); v != "" {
		cfg.DB.DBName = v
	}
	if v := os.Getenv("DB_SSLMODE"); v != "" {
		cfg.DB.SSLMode = v
	}

	// Alert config
	if v := os.Getenv("TELEGRAM_ENABLED"); v != "" {
		cfg.Alert.TelegramEnabled = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("TELEGRAM_TOKEN"); v != "" {
		cfg.Alert.TelegramToken = v
	}
	if v := os.Getenv("TELEGRAM_CHAT_ID"); v != "" {
		cfg.Alert.TelegramChatID = v
	}

	// Unused env vars loaded for completeness but not stored in config structs
	_ = os.Getenv("DASHBOARD_PORT")
	_ = os.Getenv("LOG_LEVEL")
	_ = time.Second // ensure time import is used

	return cfg
}
