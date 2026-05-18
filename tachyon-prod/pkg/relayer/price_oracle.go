package relayer

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const priceCacheTTL = 10 * time.Second

type cachedPrice struct {
	price     float64
	fetchedAt time.Time
}

type priceOracle struct {
	client *http.Client
	mu     sync.Mutex
	cache  map[string]cachedPrice
}

func newPriceOracle() *priceOracle {
	return &priceOracle{
		client: &http.Client{Timeout: 5 * time.Second},
		cache:  make(map[string]cachedPrice),
	}
}

var coinGeckoIDs = map[string]string{
	"USDC": "usd-coin",
	"USDT": "tether",
	"WETH": "weth",
	"ETH":  "ethereum",
	"ZEN":  "zencash",
	"USDM": "megausd", // MegaETH's USDm stable; CoinGecko slug confirmed 2026-05-18 ($0.99987)
}

var coinCapIDs = map[string]string{
	"USDC": "usd-coin",
	"USDT": "tether",
	"WETH": "ethereum",
	"ETH":  "ethereum",
	"ZEN":  "zencash",
}

var cryptoCompareSymbols = map[string]string{
	"USDC": "USDC",
	"USDT": "USDT",
	"WETH": "ETH",
	"ETH":  "ETH",
	"ZEN":  "ZEN",
}

// binancePairs omits ZEN and USDT — no straightforward USDT-denominated pair for these.
var binancePairs = map[string]string{
	"USDC": "USDCUSDT",
	"WETH": "ETHUSDT",
	"ETH":  "ETHUSDT",
}

// symbolAliases maps on-chain ERC20 symbols to canonical tickers the
// public oracles know. Bridged/wrapped variants are priced as their underlying.
// Lookup is done after strings.ToUpper so only uppercase keys are needed.
var symbolAliases = map[string]string{
	// USDC variants
	"USDC.E":  "USDC",
	"USDBC":   "USDC", // legacy Base USDC
	"USDC.EE": "USDC", // guard against double-suffix typos

	// USDT variants
	"USDT0":  "USDT", // MegaETH OFT USDT, 1:1 with USDT
	"USDT.E": "USDT", // bridged USDT (e.g. USDT.e on Horizen)

	// ETH variants
	"WETH": "ETH",

	// BTC variants
	"WBTC":  "BTC",
	"CBBTC": "BTC",

	// Testnet deployment tokens — priced at USDC rate ($1.00)
	"HZNA":  "USDC", // Horizen testnet token (deployment scripts)
	"BASEA": "USDC", // Base testnet token (deployment scripts)
}

// FetchConsensusPrice queries up to four APIs in parallel and returns the median
// USD price rounded to two decimal places. Results are cached for priceCacheTTL.
// Returns the single available price when only one source responds.
func (o *priceOracle) FetchConsensusPrice(symbol string) (float64, error) {
	upper := strings.ToUpper(symbol)
	if alias, ok := symbolAliases[upper]; ok {
		upper = alias
	}

	// Cache check.
	o.mu.Lock()
	if entry, ok := o.cache[upper]; ok && time.Since(entry.fetchedAt) < priceCacheTTL {
		o.mu.Unlock()
		log.WithFields(log.Fields{"symbol": upper, "price": entry.price}).Debug("Price cache hit")
		return entry.price, nil
	}
	o.mu.Unlock()

	type result struct {
		source string
		price  float64
		err    error
	}

	fetchers := []struct {
		name string
		fn   func(string) (float64, error)
	}{
		{"coingecko", o.fetchCoinGecko},
		{"coincap", o.fetchCoinCap},
		{"cryptocompare", o.fetchCryptoCompare},
		{"binance", o.fetchBinance},
	}

	ch := make(chan result, len(fetchers))
	for _, f := range fetchers {
		go func(name string, fn func(string) (float64, error)) {
			price, err := fn(upper)
			ch <- result{name, price, err}
		}(f.name, f.fn)
	}

	var prices []float64
	for range fetchers {
		r := <-ch
		if r.err != nil {
			log.WithFields(log.Fields{"source": r.source, "symbol": upper, "error": r.err}).Debug("Price source unavailable")
			continue
		}
		if r.price > 0 {
			log.WithFields(log.Fields{"source": r.source, "symbol": upper, "price": r.price}).Debug("Fetched price from source")
			prices = append(prices, r.price)
		}
	}

	if len(prices) == 0 {
		return 0, fmt.Errorf("no price data available for %s", symbol)
	}

	sorted := make([]float64, len(prices))
	copy(sorted, prices)
	sort.Float64s(sorted)
	n := len(sorted)
	var median float64
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	} else {
		median = sorted[n/2]
	}
	consensus := math.Round(median*100) / 100

	log.WithFields(log.Fields{"symbol": upper, "sources": len(prices), "consensus": consensus}).Info("Computed consensus price")

	o.mu.Lock()
	o.cache[upper] = cachedPrice{price: consensus, fetchedAt: time.Now()}
	o.mu.Unlock()

	return consensus, nil
}

func (o *priceOracle) fetchCoinGecko(symbol string) (float64, error) {
	id, ok := coinGeckoIDs[symbol]
	if !ok {
		return 0, fmt.Errorf("no CoinGecko ID for symbol %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd", id))
	if err != nil {
		return 0, fmt.Errorf("coingecko request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("coingecko returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read coingecko response: %w", err)
	}
	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, fmt.Errorf("failed to parse coingecko response: %w", err)
	}
	tokenData, ok := data[id]
	if !ok {
		return 0, fmt.Errorf("token %s not found in coingecko response", id)
	}
	price, ok := tokenData["usd"]
	if !ok {
		return 0, fmt.Errorf("USD price not found for %s in coingecko response", id)
	}
	return price, nil
}

func (o *priceOracle) fetchCoinCap(symbol string) (float64, error) {
	id, ok := coinCapIDs[symbol]
	if !ok {
		return 0, fmt.Errorf("no CoinCap ID for symbol %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://api.coincap.io/v2/assets/%s", id))
	if err != nil {
		return 0, fmt.Errorf("coincap request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("coincap returned status %d", resp.StatusCode)
	}
	var data struct {
		Data struct {
			PriceUsd string `json:"priceUsd"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("failed to parse coincap response: %w", err)
	}
	price, err := strconv.ParseFloat(data.Data.PriceUsd, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse coincap price %q: %w", data.Data.PriceUsd, err)
	}
	return price, nil
}

func (o *priceOracle) fetchCryptoCompare(symbol string) (float64, error) {
	fsym, ok := cryptoCompareSymbols[symbol]
	if !ok {
		return 0, fmt.Errorf("no CryptoCompare symbol for %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://min-api.cryptocompare.com/data/price?fsym=%s&tsyms=USD", fsym))
	if err != nil {
		return 0, fmt.Errorf("cryptocompare request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("cryptocompare returned status %d", resp.StatusCode)
	}
	var data map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("failed to parse cryptocompare response: %w", err)
	}
	price, ok := data["USD"]
	if !ok {
		return 0, fmt.Errorf("USD price not found in cryptocompare response for %s", fsym)
	}
	return price, nil
}

func (o *priceOracle) fetchBinance(symbol string) (float64, error) {
	pair, ok := binancePairs[symbol]
	if !ok {
		return 0, fmt.Errorf("no Binance pair for symbol %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://api.binance.com/api/v3/ticker/price?symbol=%s", pair))
	if err != nil {
		return 0, fmt.Errorf("binance request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("binance returned status %d", resp.StatusCode)
	}
	var data struct {
		Price string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("failed to parse binance response: %w", err)
	}
	price, err := strconv.ParseFloat(data.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse binance price %q: %w", data.Price, err)
	}
	return price, nil
}
