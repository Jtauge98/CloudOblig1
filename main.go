package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Configuration constants used throughout the service.
// These define API versioning, default server settings,
// upstream timeouts, concurrency limits, and default upstream endpoints.
const (
	apiVersion                = "v1"
	defaultPort               = "8080"
	upstreamTimeout           = 4 * time.Second
	httpClientTimeout         = 5 * time.Second
	maxNeighborConcurrency    = 6
	defaultRestCountriesURL   = "http://129.241.150.113:8080"
	defaultCurrencyServiceURL = "http://129.241.150.113:9090"
)

// Runtime variables initialized at startup.
// These include service start time, resolved upstream base URLs,
// shared HTTP client configuration, and country code validation pattern.
var (
	startTime = time.Now()

	restCountriesBaseURL = envOrDefault("RESTCOUNTRIES_BASE_URL", defaultRestCountriesURL)
	currencyBaseURL      = envOrDefault("CURRENCY_BASE_URL", defaultCurrencyServiceURL)

	httpClient = &http.Client{Timeout: httpClientTimeout}

	countryCodeRe = regexp.MustCompile(`^[A-Za-z]{2}$`)
)

// RestCountry represents the subset of fields retrieved
// from the RestCountries upstream API that are required by this service.
type RestCountry struct {
	Name struct {
		Common string `json:"common"`
	} `json:"name"`

	Continents []string          `json:"continents"`
	Population int               `json:"population"`
	Area       float64           `json:"area"`
	Languages  map[string]string `json:"languages"`
	Borders    []string          `json:"borders"`

	Flags struct {
		PNG string `json:"png"`
	} `json:"flags"`

	Capital []string `json:"capital"`

	Currencies map[string]struct {
		Name string `json:"name"`
	} `json:"currencies"`
}

// CurrencyResponse represents exchange rate data returned
// by the upstream currency service.
type CurrencyResponse struct {
	Base  string             `json:"base"`
	Rates map[string]float64 `json:"rates"`
}

// errorResponse defines the standard JSON structure
// used when returning error messages to clients.
type errorResponse struct {
	Error string `json:"error"`
}

// statusResponse represents the response payload
// for the service status endpoint.
type statusResponse struct {
	RestCountriesAPI int    `json:"restcountriesapi"`
	CurrenciesAPI    int    `json:"currenciesapi"`
	Version          string `json:"version"`
	UptimeSeconds    int    `json:"uptime"`
}

// infoResponse defines the structured country information
// returned by the info endpoint.
type infoResponse struct {
	Name       string            `json:"name"`
	Continents []string          `json:"continents"`
	Population int               `json:"population"`
	Area       float64           `json:"area"`
	Languages  map[string]string `json:"languages"`
	Borders    []string          `json:"borders"`
	FlagPNG    string            `json:"flag"`
	Capital    string            `json:"capital"`
}

// exchangeResponse defines the response structure
// returned by the exchange rates endpoint.
type exchangeResponse struct {
	Country       string             `json:"country"`
	BaseCurrency  string             `json:"base-currency"`
	ExchangeRates map[string]float64 `json:"exchange-rates"`
}

// main sets up HTTP routes and starts the web server.
// The server port is resolved from the PORT environment variable
// to support both local and cloud deployment.
func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/countryinfo/v1/status/", statusHandler)
	mux.HandleFunc("/countryinfo/v1/info/", infoHandler)
	mux.HandleFunc("/countryinfo/v1/exchange/", exchangeHandler)

	port := envOrDefault("PORT", defaultPort)

	log.Println("Server running on port", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// envOrDefault retrieves an environment variable value,
// returning a default if the variable is not set.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// writeJSON writes a JSON-encoded response with the specified HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Etter WriteHeader kan vi ikke endre status, men vi kan logge.
		log.Println("encode error:", err)
	}
}

// writeError is a convenience helper for returning standardized JSON error responses.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// pathAfter extracts the URL path segment following a specified prefix.
// Used to retrieve dynamic parameters such as country codes.
func pathAfter(prefix, path string) string {
	p := strings.TrimPrefix(path, prefix)
	return strings.Trim(p, "/")
}

// validateCode verifies that the provided country code
// matches the expected ISO alpha-2 format.
func validateCode(code string) bool {
	return countryCodeRe.MatchString(code)
}

// statusHandler reports the health of the upstream services
// and returns the current uptime of this API.
func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	restStatus := checkAPI(r.Context(), restCountriesBaseURL)
	curStatus := checkAPI(r.Context(), currencyBaseURL)

	status := http.StatusOK
	if restStatus != http.StatusOK || curStatus != http.StatusOK {
		status = http.StatusServiceUnavailable
	}

	resp := map[string]interface{}{
		"restcountriesapi": restStatus,
		"currenciesapi":    curStatus,
		"version":          apiVersion,
		"uptime":           int(time.Since(startTime).Seconds()),
	}

	writeJSON(w, status, resp)
}

// infoHandler retrieves and returns country information
// based on an ISO alpha-2 country code.
func infoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	code := pathAfter("/countryinfo/v1/info/", r.URL.Path)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing country code. Example: /countryinfo/v1/info/no",
		})
		return
	}
	if !validateCode(code) {
		writeError(w, http.StatusBadRequest, "invalid country code (expected alpha-2 or alpha-3)")
		return
	}

	country, status, err := fetchCountry(r.Context(), code)
	if err != nil {
		log.Println(err)

		if errors.Is(err, context.DeadlineExceeded) {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{
				"error": "upstream service timeout",
			})
			return
		}

		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "upstream service unavailable",
		})
		return
	}
	if status == http.StatusNotFound || country == nil {
		writeError(w, http.StatusNotFound, "country not found")
		return
	}

	capital := ""
	if len(country.Capital) > 0 {
		capital = country.Capital[0]
	}

	writeJSON(w, http.StatusOK, infoResponse{
		Name:       country.Name.Common,
		Continents: country.Continents,
		Population: country.Population,
		Area:       country.Area,
		Languages:  country.Languages,
		Borders:    country.Borders,
		FlagPNG:    country.Flags.PNG,
		Capital:    capital,
	})
}

// exchangeHandler returns exchange rates between the country's
// base currency and the currencies used by its neighboring countries.
// Neighbor data is fetched with controlled concurrency.
func exchangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	code := pathAfter("/countryinfo/v1/exchange/", r.URL.Path)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing country code. Example: /countryinfo/v1/exchange/no",
		})
		return
	}
	if !validateCode(code) {
		writeError(w, http.StatusBadRequest, "invalid country code (expected alpha-2 or alpha-3)")
		return
	}

	country, status, err := fetchCountry(r.Context(), code)
	if err != nil {
		log.Println(err)

		if errors.Is(err, context.DeadlineExceeded) {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{
				"error": "upstream service timeout",
			})
			return
		}

		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "upstream service unavailable",
		})
		return
	}
	if status == http.StatusNotFound || country == nil {
		writeError(w, http.StatusNotFound, "country not found")
		return
	}

	base := pickFirstCurrency(country.Currencies)
	if base == "" {
		writeError(w, http.StatusInternalServerError, "no currency found")
		return
	}

	if len(country.Borders) == 0 {
		writeJSON(w, http.StatusOK, exchangeResponse{
			Country:       country.Name.Common,
			BaseCurrency:  base,
			ExchangeRates: map[string]float64{},
		})
		return
	}

	neighborCurrencies := fetchNeighborCurrencies(r.Context(), country.Borders, base)

	rates, err := fetchExchangeRates(r.Context(), base)
	if err != nil {
		log.Printf("fetchExchangeRates failed: base=%s err=%v", base, err)

		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "upstream service timeout")
			return
		}

		writeError(w, http.StatusBadGateway, "failed to fetch exchange rates")
		return
	}

	out := make(map[string]float64)
	for cur := range neighborCurrencies {
		if rate, ok := rates.Rates[cur]; ok {
			out[cur] = rate
		}
	}

	writeJSON(w, http.StatusOK, exchangeResponse{
		Country:       country.Name.Common,
		BaseCurrency:  base,
		ExchangeRates: out,
	})
}

var errUpstream = errors.New("upstream error")

// fetchCountry calls the RestCountries upstream API
// and returns the first matching country result.
func fetchCountry(ctx context.Context, code string) (*RestCountry, int, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
	defer cancel()

	url := restCountriesBaseURL + "/v3.1/alpha/" + strings.ToLower(code)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, http.StatusNotFound, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, http.StatusBadGateway, errUpstream
	}

	var countries []RestCountry
	if err := json.NewDecoder(resp.Body).Decode(&countries); err != nil || len(countries) == 0 {
		return nil, http.StatusBadGateway, errUpstream
	}

	return &countries[0], http.StatusOK, nil
}

func pickFirstCurrency(currencies map[string]struct {
	Name string `json:"name"`
}) string {
	for c := range currencies {
		return c
	}
	return ""
}

// fetchNeighborCurrencies collects currencies from neighboring countries.
// Concurrency is limited using a semaphore to prevent excessive upstream calls.
func fetchNeighborCurrencies(ctx context.Context, borders []string, base string) map[string]struct{} {
	set := make(map[string]struct{})

	sem := make(chan struct{}, maxNeighborConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, border := range borders {
		border := border

		wg.Add(1)
		go func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			c, status, err := fetchCountry(ctx, border)
			if err != nil || status != http.StatusOK || c == nil {
				return
			}

			for cur := range c.Currencies {
				if cur == base {
					continue
				}
				mu.Lock()
				set[cur] = struct{}{}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return set
}

// fetchExchangeRates retrieves exchange rate data
// for a given base currency from the currency service.
func fetchExchangeRates(ctx context.Context, baseCurrency string) (CurrencyResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
	defer cancel()

	url := currencyBaseURL + "/currency/" + strings.ToUpper(baseCurrency)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CurrencyResponse{}, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return CurrencyResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CurrencyResponse{}, errUpstream
	}

	var data CurrencyResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return CurrencyResponse{}, err
	}

	return data, nil
}

// checkAPI performs a health check against an upstream service
// using a context with timeout and returns the resulting HTTP status code.
func checkAPI(ctx context.Context, url string) int {
	ctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return http.StatusInternalServerError
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return http.StatusBadGateway
	}
	defer resp.Body.Close()

	return resp.StatusCode
}
