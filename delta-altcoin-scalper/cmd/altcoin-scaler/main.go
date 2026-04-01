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

	"github.com/underachievers/delta-altcoin-scalper/internal/config"
	"github.com/underachievers/delta-altcoin-scalper/internal/delta"
	"github.com/underachievers/delta-altcoin-scalper/internal/execution"
	"github.com/underachievers/delta-altcoin-scalper/internal/indicators"
	"github.com/underachievers/delta-altcoin-scalper/internal/models"
	"github.com/underachievers/delta-altcoin-scalper/internal/risk"
	"github.com/underachievers/delta-altcoin-scalper/internal/scanner"
	sigeng "github.com/underachievers/delta-altcoin-scalper/internal/signal"
	"github.com/underachievers/delta-altcoin-scalper/internal/state"
)

// ---------------------------------------------------------------------------
// Adapter: delta.DeltaClient -> scanner.DeltaClient
// ---------------------------------------------------------------------------

type scannerDeltaAdapter struct {
	client *delta.DeltaClient
}

func (a *scannerDeltaAdapter) GetAllContracts(ctx context.Context) ([]models.Symbol, error) {
	return a.client.GetContracts()
}

func (a *scannerDeltaAdapter) GetCandles(ctx context.Context, symbol, interval string, limit int) ([]models.Candle, error) {
	return a.client.GetCandles(symbol, interval, limit)
}

func (a *scannerDeltaAdapter) GetOpenInterest(ctx context.Context, symbol string) (decimal.Decimal, error) {
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	return ticker.OpenInterest, nil
}

// ---------------------------------------------------------------------------
// Adapter: indicators package functions -> scanner.IndicatorCalculator
// ---------------------------------------------------------------------------

type indicatorCalcAdapter struct{}

func (a *indicatorCalcAdapter) CalculateRSI(closes []decimal.Decimal, period int) decimal.Decimal {
	return indicators.RSI(closes, period)
}

func (a *indicatorCalcAdapter) CalculateATR(candles []models.Candle, period int) decimal.Decimal {
	return indicators.ATR(candles, period)
}

func (a *indicatorCalcAdapter) CalculateEMA(values []decimal.Decimal, period int) decimal.Decimal {
	return indicators.EMA(values, period)
}

func (a *indicatorCalcAdapter) CalculateMACD(closes []decimal.Decimal, fastPeriod, slowPeriod, signalPeriod int) (decimal.Decimal, decimal.Decimal, decimal.Decimal) {
	return indicators.MACD(closes, fastPeriod, slowPeriod, signalPeriod)
}

func (a *indicatorCalcAdapter) CalculateROC(closes []decimal.Decimal, period int) decimal.Decimal {
	return indicators.RateOfChange(closes, period)
}

// ---------------------------------------------------------------------------
// Adapter: delta.DeltaClient -> signal.DeltaClient
// ---------------------------------------------------------------------------

type signalDeltaAdapter struct {
	client *delta.DeltaClient
}

func (a *signalDeltaAdapter) GetKlines(symbol, interval string, limit int) ([]models.Candle, error) {
	return a.client.GetCandles(symbol, interval, limit)
}

func (a *signalDeltaAdapter) GetOpenInterest(symbol string) (decimal.Decimal, error) {
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	return ticker.OpenInterest, nil
}

func (a *signalDeltaAdapter) GetOpenInterestHistory(symbol string, limit int) ([]decimal.Decimal, error) {
	result := make([]decimal.Decimal, 0, limit)
	for i := 0; i < limit; i++ {
		result = append(result, decimal.Zero)
	}
	return result, nil
}

func (a *signalDeltaAdapter) GetFundingRate(symbol string) (decimal.Decimal, error) {
	info, err := a.client.GetFundingRate(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	return info.FundingRate, nil
}

func (a *signalDeltaAdapter) GetSymbol(symbol string) (*models.Symbol, error) {
	return a.client.GetTicker(symbol)
}

// ---------------------------------------------------------------------------
// Adapter: delta.DeltaClient -> execution.ExchangeClient
// ---------------------------------------------------------------------------

type exchangeAdapter struct {
	client *delta.DeltaClient
}

func (a *exchangeAdapter) PlaceOrder(symbol string, side models.OrderSide, orderType models.OrderType, quantity, price, stopPrice decimal.Decimal) (*models.Order, error) {
	return a.client.PlaceOrder(symbol, string(side), string(orderType), quantity, price)
}

func (a *exchangeAdapter) CancelOrder(orderID string) error {
	return a.client.CancelOrder(orderID)
}

func (a *exchangeAdapter) GetOrder(orderID string) (*models.Order, error) {
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

func (a *exchangeAdapter) GetMarkPrice(symbol string) (decimal.Decimal, error) {
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	return ticker.MarkPrice, nil
}

func (a *exchangeAdapter) GetAccountBalance() (decimal.Decimal, error) {
	balances, err := a.client.GetBalance()
	if err != nil {
		return decimal.Zero, err
	}
	if len(balances) > 0 {
		return balances[0].LastPrice, nil
	}
	return decimal.Zero, fmt.Errorf("no balance found")
}

func (a *exchangeAdapter) GetOpenPositions() ([]models.Position, error) {
	return a.client.GetPositions()
}

func (a *exchangeAdapter) ClosePosition(symbol string, quantity decimal.Decimal) (decimal.Decimal, error) {
	ticker, err := a.client.GetTicker(symbol)
	if err != nil {
		return decimal.Zero, err
	}
	side := models.OrderSideSell
	pos, err := a.client.GetPositions()
	if err == nil && len(pos) > 0 {
		for _, p := range pos {
			if p.Symbol == symbol && p.Side == models.OrderSideSell {
				side = models.OrderSideBuy
				break
			}
		}
	}
	order, err := a.client.PlaceOrder(symbol, string(side), "market", quantity, ticker.MarkPrice)
	if err != nil {
		return decimal.Zero, err
	}
	return order.AvgFillPrice, nil
}

// ---------------------------------------------------------------------------
// Adapter: risk.Manager -> execution.RiskManager
// ---------------------------------------------------------------------------

type riskManagerAdapter struct {
	mgr      *risk.Manager
	stateMgr *state.Manager
}

func (a *riskManagerAdapter) CalculatePositionSize(sig models.Signal, balance decimal.Decimal, leverage int) (decimal.Decimal, error) {
	sizing, err := a.mgr.CalculatePositionSize(sig.EntryPrice, sig.Indicators.ATR, balance)
	if err != nil {
		return decimal.Zero, err
	}
	return sizing.PositionSize, nil
}

func (a *riskManagerAdapter) CheckPreFlight(sig models.Signal, balance decimal.Decimal, openPositions int, dailyPnL decimal.Decimal) error {
	canOpen, reason := a.mgr.CanOpenPosition()
	if !canOpen {
		return fmt.Errorf("cannot open position: %s", reason)
	}
	return nil
}

func (a *riskManagerAdapter) CalculateStopLoss(entryPrice decimal.Decimal, atr decimal.Decimal) decimal.Decimal {
	return a.mgr.CalculateStopLoss(entryPrice, atr, models.OrderSideBuy)
}

func (a *riskManagerAdapter) CalculateTakeProfit(entryPrice decimal.Decimal, atr decimal.Decimal, multiplier float64) decimal.Decimal {
	dist := atr.Mul(decimal.NewFromFloat(multiplier))
	return entryPrice.Add(dist)
}

func (a *riskManagerAdapter) DetermineLeverage(sig models.Signal) int {
	return 5
}

// ---------------------------------------------------------------------------
// Adapter: state.Manager -> execution.TradeRecorder
// ---------------------------------------------------------------------------

type tradeRecorderAdapter struct {
	mgr *state.Manager
}

func (a *tradeRecorderAdapter) RecordTrade(trade models.Trade) error {
	a.mgr.RecordTrade(trade)
	return nil
}

func (a *tradeRecorderAdapter) UpdateDailyStats(stats models.DailyStats) error {
	return nil
}

// ---------------------------------------------------------------------------
// Signal channel adapter: *models.Signal -> models.Signal
// ---------------------------------------------------------------------------

func signalAdapterLoop(ctx context.Context, in <-chan *models.Signal, out chan models.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-in:
			if !ok {
				close(out)
				return
			}
			if sig != nil {
				select {
				case out <- *sig:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// DashboardServer - serves live data from all components
// ---------------------------------------------------------------------------

type DashboardServer struct {
	stateMgr   *state.Manager
	scanner    *scanner.CoinScanner
	sigEngine  *sigeng.Engine
	riskMgr    *risk.Manager
	execEngine *execution.Engine
}

func NewDashboardServer(
	stateMgr *state.Manager,
	scanner *scanner.CoinScanner,
	sigEngine *sigeng.Engine,
	riskMgr *risk.Manager,
	execEngine *execution.Engine,
) *DashboardServer {
	return &DashboardServer{
		stateMgr:   stateMgr,
		scanner:    scanner,
		sigEngine:  sigEngine,
		riskMgr:    riskMgr,
		execEngine: execEngine,
	}
}

func (d *DashboardServer) Start(addr string) error {
	http.HandleFunc("/api/status", d.handleStatus)
	http.HandleFunc("/api/positions", d.handlePositions)
	http.HandleFunc("/api/scanner", d.handleScanner)
	http.HandleFunc("/api/daily", d.handleDaily)
	log.Printf("[Dashboard] serving on %s", addr)
	return http.ListenAndServe(addr, nil)
}

func (d *DashboardServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"running":true,"capital":"%s"}`, d.stateMgr.GetCurrentCapital().String())
}

func (d *DashboardServer) handlePositions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	positions := d.stateMgr.GetOpenPositions()
	fmt.Fprintf(w, `{"positions":%d,"symbols":["%s"]}`, len(positions), joinStrings(positions))
}

func (d *DashboardServer) handleScanner(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	results := d.scanner.GetResults()
	fmt.Fprintf(w, `{"results":%d}`, len(results))
}

func (d *DashboardServer) handleDaily(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := d.stateMgr.GetDailyStats()
	fmt.Fprintf(w, `{"pnl":"%s","trades":%d}`, stats.PnL.String(), stats.TradesCount)
}

func joinStrings(s []string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += ","
		}
		result += v
	}
	return result
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	configPath := flag.String("config", "", "path to config file (optional)")
	flag.Parse()

	log.Println("[main] Delta Exchange Altcoin Futures Scalping Bot starting")

	settings := config.DefaultSettings()

	if *configPath != "" {
		log.Printf("[main] config file override not yet implemented, using defaults: %s", *configPath)
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

	deltaClient := delta.NewClient(settings)

	stateMgr := state.NewManager(settings)

	scannerAdapter := &scannerDeltaAdapter{client: deltaClient}
	indicatorAdapter := &indicatorCalcAdapter{}
	coinScanner := scanner.NewCoinScanner(scannerAdapter, indicatorAdapter, settings)

	signalAdapter := &signalDeltaAdapter{client: deltaClient}
	sigEngine := sigeng.NewEngine(settings, signalAdapter, (*scanner.Scanner)(coinScanner))

	riskMgr := risk.NewManager(settings, decimal.NewFromFloat(settings.Risk.InitialCapital))
	riskAdapter := &riskManagerAdapter{mgr: riskMgr, stateMgr: stateMgr}

	tradeRecorder := &tradeRecorderAdapter{mgr: stateMgr}

	signalOut := make(chan models.Signal, 64)
	go signalAdapterLoop(ctx, sigEngine.SignalChannel(), signalOut)

	exchangeAdapter := &exchangeAdapter{client: deltaClient}
	execEngine := execution.NewEngine(settings, exchangeAdapter, riskAdapter, tradeRecorder, signalOut)

	dashboard := NewDashboardServer(stateMgr, coinScanner, sigEngine, riskMgr, execEngine)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		coinScanner.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sigEngine.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := execEngine.Start(ctx); err != nil {
			log.Printf("[main] execution engine error: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := dashboard.Start(":8080"); err != nil && err != http.ErrServerClosed {
			log.Printf("[main] dashboard error: %v", err)
		}
	}()

	log.Println("[main] all components started, waiting for shutdown signal")

	<-ctx.Done()

	log.Println("[main] shutdown initiated, stopping components")

	sigEngine.Stop()
	_ = execEngine.Stop()
	coinScanner.Stop()

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

	if err := stateMgr.Save("state.json"); err != nil {
		log.Printf("[main] failed to save state: %v", err)
	}

	log.Println("[main] Delta Exchange Altcoin Futures Scalping Bot stopped")
}
