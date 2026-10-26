package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	yahooChartURL   = "https://query1.finance.yahoo.com/v8/finance/chart"
	maxResponseSize = 64 << 20
)

type dailyPrice struct {
	Date     time.Time
	Open     float64
	Close    float64
	AdjClose float64
}

type corporateAction struct {
	SourceID    string
	Date        time.Time
	Type        string
	Amount      float64
	Numerator   float64
	Denominator float64
}

type yahooResult struct {
	Prices  []dailyPrice
	Actions []corporateAction
}

type chartEvents struct {
	Dividends map[string]struct {
		Amount float64 `json:"amount"`
		Date   int64   `json:"date"`
	} `json:"dividends"`
	Splits map[string]struct {
		Date        int64   `json:"date"`
		Numerator   float64 `json:"numerator"`
		Denominator float64 `json:"denominator"`
	} `json:"splits"`
}

type yahooClient struct {
	httpClient  *http.Client
	baseURL     string
	userAgent   string
	pacer       *pacer
	maxAttempts int
	logger      *slog.Logger
}

func newYahooClient(httpClient *http.Client, baseURL string, logger *slog.Logger) *yahooClient {
	return &yahooClient{
		httpClient:  httpClient,
		baseURL:     baseURL,
		userAgent:   "chopstick-yahoo-prices/1.0",
		pacer:       newPacer(2 * time.Second),
		maxAttempts: 3,
		logger:      logger,
	}
}

type pacer struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newPacer(interval time.Duration) *pacer {
	return &pacer{interval: interval}
}

func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()

	wait := max(time.Until(p.next), 0)

	p.next = time.Now().Add(wait + p.interval)
	p.mu.Unlock()

	if wait == 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for Yahoo request pacing: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func (client *yahooClient) fetch(
	ctx context.Context, ticker string, from, to time.Time,
) (yahooResult, error) {
	requestURL, err := client.requestURL(ticker, from, to)
	if err != nil {
		return yahooResult{}, err
	}

	var lastErr error

	for attempt := 1; attempt <= client.maxAttempts; attempt++ {
		if err := client.pacer.wait(ctx); err != nil {
			return yahooResult{}, err
		}

		raw, retry, err := client.request(ctx, requestURL)
		if err == nil {
			result, parseErr := parseYahooResponse(ticker, raw, from, to)
			if parseErr != nil {
				return yahooResult{}, parseErr
			}

			return result, nil
		}

		lastErr = err

		if !retry || attempt == client.maxAttempts {
			break
		}

		client.logger.Warn(
			"retrying Yahoo request",
			"ticker", ticker,
			"attempt", attempt,
			"error", err,
		)
	}

	return yahooResult{}, fmt.Errorf(
		"fetch Yahoo prices for %s: %w", ticker, lastErr,
	)
}

func (client *yahooClient) requestURL(ticker string, from, to time.Time) (string, error) {
	if strings.TrimSpace(ticker) == "" {
		return "", errors.New("empty Yahoo ticker")
	}

	if to.Before(from) {
		return "", errors.New("yahoo end date is before start date")
	}

	base, err := url.Parse(client.baseURL + "/" + url.PathEscape(ticker))
	if err != nil {
		return "", fmt.Errorf("build Yahoo URL: %w", err)
	}

	query := base.Query()
	query.Set("period1", strconv.FormatInt(from.Unix(), 10))
	query.Set("period2", strconv.FormatInt(to.AddDate(0, 0, 1).Unix(), 10))
	query.Set("interval", "1d")
	query.Set("events", "div,splits")
	base.RawQuery = query.Encode()

	return base.String(), nil
}

func (client *yahooClient) request(
	ctx context.Context, requestURL string,
) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("create Yahoo request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", client.userAgent)

	response, err := client.httpClient.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("send Yahoo request: %w", err)
	}

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	closeErr := response.Body.Close()

	if err != nil {
		return nil, true, fmt.Errorf("read Yahoo response: %w", err)
	}

	if closeErr != nil {
		return raw, true, fmt.Errorf("close Yahoo response: %w", closeErr)
	}

	if len(raw) > maxResponseSize {
		return nil, false, fmt.Errorf(
			"yahoo response exceeds %d bytes", maxResponseSize,
		)
	}

	if response.StatusCode != http.StatusOK {
		return raw, retryableStatus(response.StatusCode), fmt.Errorf(
			"yahoo HTTP %s: %s", response.Status,
			responseExcerpt(raw),
		)
	}

	return raw, false, nil
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func responseExcerpt(raw []byte) string {
	const excerptLength = 256

	value := strings.TrimSpace(string(raw))
	if len(value) > excerptLength {
		value = value[:excerptLength]
	}

	return value
}

type chartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency string `json:"currency"`
				Symbol   string `json:"symbol"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open  []*float64 `json:"open"`
					Close []*float64 `json:"close"`
				} `json:"quote"`
				AdjClose []struct {
					Values []*float64 `json:"adjclose"`
				} `json:"adjclose"`
			} `json:"indicators"`
			Events chartEvents `json:"events"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func parseYahooResponse(
	ticker string,
	raw []byte,
	from time.Time,
	to time.Time,
) (yahooResult, error) {
	var data chartResponse
	if err := json.Unmarshal(raw, &data); err != nil {
		return yahooResult{}, fmt.Errorf(
			"decode Yahoo response for %s: %w", ticker, err,
		)
	}

	if data.Chart.Error != nil {
		return yahooResult{}, fmt.Errorf(
			"yahoo %s for %s: %s", data.Chart.Error.Code, ticker,
			data.Chart.Error.Description,
		)
	}

	if len(data.Chart.Result) != 1 {
		return yahooResult{}, fmt.Errorf(
			"yahoo returned %d results for %s, expected one",
			len(data.Chart.Result), ticker,
		)
	}

	result := data.Chart.Result[0]
	if !strings.EqualFold(result.Meta.Currency, "USD") {
		return yahooResult{}, fmt.Errorf(
			"yahoo returned currency %q for %s, expected USD",
			result.Meta.Currency, ticker,
		)
	}

	if !strings.EqualFold(result.Meta.Symbol, ticker) {
		return yahooResult{}, fmt.Errorf(
			"yahoo returned symbol %q for requested ticker %s",
			result.Meta.Symbol, ticker,
		)
	}

	want := len(result.Timestamp)
	if want == 0 {
		if len(result.Indicators.Quote) == 0 &&
			len(result.Indicators.AdjClose) == 0 {
			actions, err := parseYahooActions(result.Events, from, to)

			return yahooResult{Actions: actions}, err
		}

		if len(result.Indicators.Quote) == 1 &&
			len(result.Indicators.AdjClose) == 1 &&
			len(result.Indicators.Quote[0].Open) == 0 &&
			len(result.Indicators.Quote[0].Close) == 0 &&
			len(result.Indicators.AdjClose[0].Values) == 0 {
			actions, err := parseYahooActions(result.Events, from, to)

			return yahooResult{Actions: actions}, err
		}

		return yahooResult{}, fmt.Errorf(
			"yahoo returned values without timestamps for %s", ticker,
		)
	}

	if len(result.Indicators.Quote) != 1 {
		return yahooResult{}, fmt.Errorf(
			"yahoo returned %d quote series for %s, expected one",
			len(result.Indicators.Quote), ticker,
		)
	}

	if len(result.Indicators.AdjClose) != 1 {
		return yahooResult{}, fmt.Errorf(
			"yahoo returned %d adjusted-close series for %s, expected one",
			len(result.Indicators.AdjClose), ticker,
		)
	}

	quote := result.Indicators.Quote[0]
	adjusted := result.Indicators.AdjClose[0].Values

	if len(quote.Open) != want || len(quote.Close) != want ||
		len(adjusted) != want {
		return yahooResult{}, fmt.Errorf(
			"yahoo series length mismatch for %s: timestamps=%d open=%d "+
				"close=%d adjusted_close=%d",
			ticker, want, len(quote.Open), len(quote.Close), len(adjusted),
		)
	}

	prices := make([]dailyPrice, 0, want)

	var previousDate string

	for index, timestamp := range result.Timestamp {
		date := time.Unix(timestamp, 0).UTC()
		if !date.Before(to.AddDate(0, 0, 1)) {
			return yahooResult{}, fmt.Errorf(
				"yahoo date %s for %s is after the requested range",
				date.Format(time.RFC3339), ticker,
			)
		}

		observationDate := date.Format(dateLayout)
		if previousDate != "" && observationDate <= previousDate {
			return yahooResult{}, fmt.Errorf(
				"yahoo dates for %s are duplicate or unordered at %s",
				ticker, date.Format(time.RFC3339),
			)
		}

		if err := validatePriceValues(
			ticker, date, quote.Open[index], quote.Close[index], adjusted[index],
		); err != nil {
			return yahooResult{}, err
		}

		previousDate = observationDate

		if date.Before(from) {
			continue
		}

		prices = append(prices, dailyPrice{
			Date:     date,
			Open:     *quote.Open[index],
			Close:    *quote.Close[index],
			AdjClose: *adjusted[index],
		})
	}

	actions, err := parseYahooActions(result.Events, from, to)
	if err != nil {
		return yahooResult{}, fmt.Errorf(
			"validate Yahoo actions for %s: %w", ticker, err,
		)
	}

	return yahooResult{Prices: prices, Actions: actions}, nil
}

func parseYahooActions(
	events chartEvents,
	from time.Time,
	to time.Time,
) ([]corporateAction, error) {
	actions := make([]corporateAction, 0, len(events.Dividends)+len(events.Splits))

	for sourceID, dividend := range events.Dividends {
		date := time.Unix(dividend.Date, 0).UTC()
		if date.Before(from) || !date.Before(to.AddDate(0, 0, 1)) {
			continue
		}

		if dividend.Amount <= 0 || math.IsNaN(dividend.Amount) ||
			math.IsInf(dividend.Amount, 0) {
			return nil, fmt.Errorf("invalid dividend on %s", date.Format(dateLayout))
		}

		actions = append(actions, corporateAction{
			SourceID: "dividend:" + sourceID,
			Date:     date,
			Type:     "dividend",
			Amount:   dividend.Amount,
		})
	}

	for sourceID, split := range events.Splits {
		date := time.Unix(split.Date, 0).UTC()
		if date.Before(from) || !date.Before(to.AddDate(0, 0, 1)) {
			continue
		}

		if split.Numerator <= 0 || split.Denominator <= 0 ||
			math.IsNaN(split.Numerator) || math.IsNaN(split.Denominator) ||
			math.IsInf(split.Numerator, 0) || math.IsInf(split.Denominator, 0) {
			return nil, fmt.Errorf("invalid split on %s", date.Format(dateLayout))
		}

		actions = append(actions, corporateAction{
			SourceID:    "split:" + sourceID,
			Date:        date,
			Type:        "split",
			Numerator:   split.Numerator,
			Denominator: split.Denominator,
		})
	}

	sort.Slice(actions, func(left, right int) bool {
		if actions[left].Date.Equal(actions[right].Date) {
			return actions[left].SourceID < actions[right].SourceID
		}

		return actions[left].Date.Before(actions[right].Date)
	})

	return actions, nil
}

func validatePriceValues(ticker string, date time.Time, open, closeValue, adjusted *float64) error {
	values := []struct {
		name  string
		value *float64
	}{
		{name: "open", value: open},
		{name: "close", value: closeValue},
		{name: "adjusted close", value: adjusted},
	}

	for _, item := range values {
		if item.value == nil {
			return fmt.Errorf(
				"yahoo %s is null for %s on %s",
				item.name, ticker, date.Format(dateLayout),
			)
		}

		if *item.value <= 0 || math.IsNaN(*item.value) ||
			math.IsInf(*item.value, 0) {
			return fmt.Errorf(
				"yahoo %s is invalid for %s on %s: %v",
				item.name, ticker, date.Format(dateLayout), *item.value,
			)
		}
	}

	return nil
}
