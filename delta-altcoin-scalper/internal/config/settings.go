package config

import "time"

// DeltaConfig holds API connection settings
type DeltaConfig struct {
	BaseURL        string
	WebSocketURL   string
	APIKey         string
	APISecret      string
	Testnet        bool
	RequestTimeout time.Duration
	MaxRetries     int
}

// RiskConfig holds risk management settings
type RiskConfig struct {
	InitialCapital      float64
	RiskPerTradePercent float64
	MaxDailyLossPercent float64
	MaxConcurrentTrades int
	MaxTradesPerDay     int
	MinLeverage         int
	MaxLeverage         int
	DefaultLeverage     int
}

// TradeConfig holds trading logic settings
type TradeConfig struct {
	MinVolumeSurgeRatio    float64
	MinATRPercent          float64
	MinSignalScore         int
	EntryOrderTimeout      time.Duration
	FillPollInterval       time.Duration
	FillPollMaxCycles      int
	SLATRMultiplier        float64
	TP1ATRMultiplier       float64
	TP2ATRMultiplier       float64
	TP1CloseFraction       float64
	MaxTradeDuration       time.Duration
	ScannerRefreshInterval time.Duration
	SignalCheckInterval    time.Duration
	ExcludedSymbols        []string
	Min24HVolumeUSD        float64
	Max24HVolumeUSD        float64
}

// DBConfig holds database connection settings
type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// AlertConfig holds notification settings
type AlertConfig struct {
	TelegramEnabled bool
	TelegramToken   string
	TelegramChatID  string
}

// Settings holds all configuration for the scalping system
type Settings struct {
	Delta DeltaConfig
	Risk  RiskConfig
	Trade TradeConfig
	DB    DBConfig
	Alert AlertConfig
}

// DefaultSettings returns a Settings struct with all default values
func DefaultSettings() Settings {
	return Settings{
		Delta: DeltaConfig{
			BaseURL:        "https://testnet-api.delta.exchange",
			WebSocketURL:   "wss://testnet-socket.delta.exchange",
			APIKey:         "",
			APISecret:      "",
			Testnet:        true,
			RequestTimeout: 10 * time.Second,
			MaxRetries:     3,
		},
		Risk: RiskConfig{
			InitialCapital:      200,
			RiskPerTradePercent: 1.0,
			MaxDailyLossPercent: 5.0,
			MaxConcurrentTrades: 3,
			MaxTradesPerDay:     20,
			MinLeverage:         3,
			MaxLeverage:         10,
			DefaultLeverage:     5,
		},
		Trade: TradeConfig{
			MinVolumeSurgeRatio:    2.5,
			MinATRPercent:          3.0,
			MinSignalScore:         6,
			EntryOrderTimeout:      5 * time.Second,
			FillPollInterval:       500 * time.Millisecond,
			FillPollMaxCycles:      10,
			SLATRMultiplier:        1.5,
			TP1ATRMultiplier:       2.0,
			TP2ATRMultiplier:       4.0,
			TP1CloseFraction:       0.50,
			MaxTradeDuration:       15 * time.Minute,
			ScannerRefreshInterval: 15 * time.Minute,
			SignalCheckInterval:    30 * time.Second,
			ExcludedSymbols:        []string{"BTC", "ETH", "USDT", "USDC", "BTCDOM"},
			Min24HVolumeUSD:        2_000_000,
			Max24HVolumeUSD:        200_000_000,
		},
		DB: DBConfig{
			Host:     "localhost",
			Port:     "5432",
			User:     "postgres",
			Password: "postgres",
			DBName:   "delta_scalper",
			SSLMode:  "disable",
		},
		Alert: AlertConfig{
			TelegramEnabled: false,
			TelegramToken:   "",
			TelegramChatID:  "",
		},
	}
}
