package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/underachievers/delta-altcoin-scalper/dashboard/backend"
	"github.com/underachievers/delta-altcoin-scalper/internal/config"
	"github.com/underachievers/delta-altcoin-scalper/internal/delta"
	"github.com/underachievers/delta-altcoin-scalper/internal/execution"
	"github.com/underachievers/delta-altcoin-scalper/internal/indicators"
	"github.com/underachievers/delta-altcoin-scalper/internal/models"
	"github.com/underachievers/delta-altcoin-scalper/internal/risk"
	"github.com/underachievers/delta-altcoin-scalper/internal/scanner"
	sigeng "github.com/underachievers/delta-altcoin-scalper/internal/signal"
	"github.com/underachievers/delta-altcoin-scalper/internal/state"
	"github.com/underachievers/delta-altcoin-scalper/internal/telegram"
)

// ---------------------------------------------------------------------------
// Adapter: delta.DeltaClient -> scanner.DeltaClient
// ---------------------------------------------------------------------------

type ScannerDeltaClient struct {
	client *delta.DeltaClient
}

func (a *ScannerDeltaClient) GetContracts() ([]models.Symbol, error) {
	return a.client.GetContracts()
}

func (a *ScannerDeltaClient) GetTicker(symbol string) (*models.Symbol, error) {
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return nil, err
	}
	return &models.Symbol{
		Name:           symbol,
		LastPrice:      ticker.LastPrice,
		Volume24h:      ticker.Volume24h,
		Volume24hUSD:   ticker.Volume24hUSD,
		OpenInterest:   ticker.OpenInterest,
		FundingRate:    ticker.FundingRate,
		MarkPrice:      ticker.MarkPrice,
		IndexPrice:     ticker.IndexPrice,
		PriceChange24h: ticker.PriceChange24h,
	}, nil
}

func (a *ScannerDeltaClient) GetCandles(symbol, interval string, limit int) ([]models.Candle, error) {
	return a.client.GetCandles(symbol, interval, limit)
}

func (a *ScannerDeltaClient) GetOrderBook(symbol string, depth int) (*models.OrderBook, error) {
	return a.client.GetOrderBook(symbol, depth)
}

// ---------------------------------------------------------------------------
// Adapter: indicators package functions -> scanner.IndicatorCalculator
// ---------------------------------------------------------------------------

type IndicatorCalculator struct{}

func (a *IndicatorCalculator) ATR(candles []models.Candle, period int) decimal.Decimal {
	return indicators.ATR(candles, period)
}

func (a *IndicatorCalculator) RSI(closes []decimal.Decimal, period int) decimal.Decimal {
	return indicators.RSI(closes, period)
}

func (a *IndicatorCalculator) EMA(closes []decimal.Decimal, period int) decimal.Decimal {
	return indicators.EMA(closes, period)
}

func (a *IndicatorCalculator) VolumeAverage(volumes []decimal.Decimal, period int) decimal.Decimal {
	return indicators.VolumeAverage(volumes, period)
}

func (a *IndicatorCalculator) RateOfChange(closes []decimal.Decimal, lookback int) decimal.Decimal {
	return indicators.RateOfChange(closes, lookback)
}

// ---------------------------------------------------------------------------
// Adapter: delta.DeltaClient -> signal.DeltaClient
// ---------------------------------------------------------------------------

type SignalDeltaClient struct {
	client *delta.DeltaClient
}

func (a *SignalDeltaClient) GetCandles(symbol, interval string, limit int) ([]models.Candle, error) {
	return a.client.GetCandles(symbol, interval, limit)
}

func (a *SignalDeltaClient) GetOrderBook(symbol string, depth int) (*models.OrderBook, error) {
	return a.client.GetOrderBook(symbol, depth)
}

func (a *SignalDeltaClient) GetFundingRate(symbol string) (decimal.Decimal, error) {
	return a.client.GetFundingRate(symbol)
}

func (a *SignalDeltaClient) GetOpenInterest(symbol string) (decimal.Decimal, error) {
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	return ticker.OpenInterest, nil
}

// ---------------------------------------------------------------------------
// Adapter: scanner.CoinScanner -> signal.Scanner
// ---------------------------------------------------------------------------

type ScannerAdapter struct {
	inner *scanner.CoinScanner
}

func (a *ScannerAdapter) GetResults() []sigeng.ScannerResult {
	inner := a.inner.GetResults()
	results := make([]sigeng.ScannerResult, len(inner))
	for i, r := range inner {
		results[i] = sigeng.ScannerResult{
			Symbol: r.Symbol,
			Score:  int(r.Score.IntPart()),
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// Adapter: delta.DeltaClient -> execution.ExchangeClient
// ---------------------------------------------------------------------------

type ExecutionExchange struct {
	client   *delta.DeltaClient
	paper    *execution.PaperTradingClient
	isPaper  bool
	mu       sync.Mutex
	orderSeq int
}

func (a *ExecutionExchange) PlaceOrder(symbol, side, orderType string, quantity, price, stopPrice decimal.Decimal) (string, error) {
	if a.isPaper && a.paper != nil {
		return a.paper.PlaceOrder(symbol, side, orderType, quantity, price, stopPrice)
	}
	a.mu.Lock()
	a.orderSeq++
	id := fmt.Sprintf("live-%s-%d", symbol, a.orderSeq)
	a.mu.Unlock()

	order, err := a.client.PlaceOrder(symbol, side, orderType, quantity, price, stopPrice)
	if err != nil {
		return "", err
	}
	if order != nil && order.ID != "" {
		return order.ID, nil
	}
	return id, nil
}

func (a *ExecutionExchange) CancelOrder(orderID string) error {
	if a.isPaper && a.paper != nil {
		return a.paper.CancelOrder(orderID)
	}
	return a.client.CancelOrder(orderID)
}

func (a *ExecutionExchange) GetOrder(orderID string) (*models.Order, error) {
	if a.isPaper && a.paper != nil {
		return a.paper.GetOrder(orderID)
	}
	orders, err := a.client.GetOpenOrders()
	if err != nil {
		return nil, err
	}
	for i := range orders {
		if orders[i].ID == orderID {
			return &orders[i], nil
		}
	}
	return nil, fmt.Errorf("order %s not found", orderID)
}

func (a *ExecutionExchange) GetMarkPrice(symbol string) (decimal.Decimal, error) {
	if a.isPaper && a.paper != nil {
		return a.paper.GetMarkPrice(symbol)
	}
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	if !ticker.MarkPrice.IsZero() {
		return ticker.MarkPrice, nil
	}
	return ticker.LastPrice, nil
}

func (a *ExecutionExchange) GetAccountBalance() (decimal.Decimal, error) {
	if a.isPaper && a.paper != nil {
		return a.paper.GetAccountBalance()
	}
	return a.client.GetBalance()
}

func (a *ExecutionExchange) GetOpenPositions() ([]*models.Position, error) {
	if a.isPaper && a.paper != nil {
		return a.paper.GetOpenPositions()
	}
	positions, err := a.client.GetPositions()
	if err != nil {
		return nil, err
	}
	result := make([]*models.Position, len(positions))
	for i := range positions {
		result[i] = &positions[i]
	}
	return result, nil
}

func (a *ExecutionExchange) ClosePosition(symbol string, quantity decimal.Decimal) (decimal.Decimal, error) {
	if a.isPaper && a.paper != nil {
		return a.paper.ClosePosition(symbol, quantity)
	}
	positions, err := a.client.GetPositions()
	if err != nil {
		return decimal.Zero, err
	}
	side := string(models.OrderSideSell)
	for _, p := range positions {
		if p.Symbol == symbol {
			if p.Side == models.OrderSideSell {
				side = string(models.OrderSideBuy)
			}
			break
		}
	}
	order, err := a.client.PlaceOrder(symbol, side, string(models.OrderTypeMarket), quantity, decimal.Zero, decimal.Zero)
	if err != nil {
		return decimal.Zero, err
	}
	if order != nil && !order.AvgFillPrice.IsZero() {
		return order.AvgFillPrice, nil
	}
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	return ticker.LastPrice, nil
}

// ---------------------------------------------------------------------------
// Adapter: risk.Manager -> execution.RiskManager
// ---------------------------------------------------------------------------

type RiskManagerAdapter struct {
	mgr *risk.Manager
}

func (a *RiskManagerAdapter) CalculatePositionSize(capital, entryPrice decimal.Decimal) decimal.Decimal {
	qty, _ := a.mgr.CalculatePositionSize(entryPrice, decimal.NewFromFloat(1.0))
	return qty
}

func (a *RiskManagerAdapter) CalculateStopLoss(entryPrice decimal.Decimal, side models.OrderSide, atr decimal.Decimal) decimal.Decimal {
	return a.mgr.CalculateStopLoss(entryPrice, atr, side)
}

func (a *RiskManagerAdapter) CalculateTakeProfit(entryPrice decimal.Decimal, side models.OrderSide, atr decimal.Decimal, level int) decimal.Decimal {
	return a.mgr.CalculateTakeProfit(entryPrice, atr, side, level)
}

func (a *RiskManagerAdapter) CanEnterTrade(symbol string, capital decimal.Decimal, tradeCount int) bool {
	return a.mgr.CanEnterTrade() == nil
}

// ---------------------------------------------------------------------------
// Adapter: state.Manager -> execution.TradeRecorder
// ---------------------------------------------------------------------------

type StateRecorderAdapter struct {
	mgr *state.Manager
}

func (a *StateRecorderAdapter) RecordTrade(trade *models.Trade) error {
	a.mgr.RecordTrade(*trade)
	return nil
}

func (a *StateRecorderAdapter) UpdateCapital(capital decimal.Decimal) error {
	current := a.mgr.GetCurrentCapital()
	pnl := capital.Sub(current)
	a.mgr.UpdateCapital(pnl)
	return nil
}

func (a *StateRecorderAdapter) AddPosition(pos *models.Position) error {
	a.mgr.AddPosition(pos.Symbol)
	return nil
}

func (a *StateRecorderAdapter) RemovePosition(symbol string) error {
	a.mgr.RemovePosition(symbol)
	return nil
}

func (a *StateRecorderAdapter) GetTodayTradeCount() int {
	return a.mgr.GetTodayTradeCount()
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	configPath := flag.String("config", "", "path to config file")
	paperTrading := flag.Bool("paper", true, "enable paper trading mode")
	flag.Parse()

	log.Println("[main] Delta Exchange Altcoin Futures Scalping System starting")

	cfg := config.LoadEnv()

	if *configPath != "" {
		log.Printf("[main] config file specified: %s (env vars take precedence)", *configPath)
	}

	paperMode := *paperTrading
	if paperMode {
		log.Println("[main] paper trading mode enabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("[main] received signal %v, initiating graceful shutdown", sig)
		cancel()
	}()

	log.Println("[main] initializing components")

	stateMgr := state.NewManager(&cfg)

	deltaClient := delta.NewClient(&cfg)

	if err := deltaClient.Connect(); err != nil {
		log.Printf("[main] warning: websocket connection failed: %v", err)
	}
	defer deltaClient.Disconnect()

	scannerClient := &ScannerDeltaClient{client: deltaClient}
	indicatorCalc := &IndicatorCalculator{}
	coinScanner := scanner.NewCoinScanner(scannerClient, indicatorCalc, &cfg)

	signalClient := &SignalDeltaClient{client: deltaClient}
	scannerAdapter := &ScannerAdapter{inner: coinScanner}
	sigEngine := sigeng.NewEngine(signalClient, scannerAdapter, &cfg)

	riskMgr := risk.NewManager(stateMgr, &cfg)
	riskAdapter := &RiskManagerAdapter{mgr: riskMgr}
	stateAdapter := &StateRecorderAdapter{mgr: stateMgr}

	var exchangeClient execution.ExchangeClient
	var paperClient *execution.PaperTradingClient

	if paperMode {
		paperClient = execution.NewPaperTradingClient(&cfg)
		exchangeClient = &ExecutionExchange{
			client:  deltaClient,
			paper:   paperClient,
			isPaper: true,
		}
	} else {
		exchangeClient = &ExecutionExchange{
			client:  deltaClient,
			paper:   nil,
			isPaper: false,
		}
	}

	signalCh := sigEngine.SignalsChannel()
	execEngine := execution.NewEngine(exchangeClient, riskAdapter, stateAdapter, signalCh, &cfg)

	alerter := telegram.NewAlerter(
		cfg.Alert.TelegramToken,
		cfg.Alert.TelegramChatID,
		cfg.Alert.TelegramEnabled,
	)

	dashboardServer := backend.NewServer(stateMgr, nil, nil, nil, nil, nil, nil)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		coinScanner.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sigEngine.Run()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		execEngine.Run()
	}()

	if cfg.Alert.TelegramEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alerter.Start()
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		fs := http.FileServer(http.Dir("dashboard/static"))
		http.Handle("/", fs)
		http.Handle("/api/", dashboardServer)
		log.Printf("[main] dashboard serving static files and API on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil && err != http.ErrServerClosed {
			log.Printf("[main] dashboard error: %v", err)
		}
	}()

	log.Println("[main] all components started, waiting for shutdown signal")

	<-ctx.Done()

	log.Println("[main] shutdown initiated, stopping components")

	execEngine.Stop()
	coinScanner.Stop()
	alerter.Stop()

	if paperClient != nil {
		tradeLog := paperClient.GetTradeLog()
		log.Printf("[main] paper trading session complete, total trades: %d", len(tradeLog))
	}

	log.Println("[main] waiting for goroutines to finish")

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("[main] all goroutines finished")
	case <-time.After(10 * time.Second):
		log.Println("[main] timeout waiting for goroutines, forcing exit")
	}

	if err := stateMgr.SaveState(); err != nil {
		log.Printf("[main] failed to save state: %v", err)
	}

	log.Println("[main] Delta Exchange Altcoin Futures Scalping System stopped")
}
