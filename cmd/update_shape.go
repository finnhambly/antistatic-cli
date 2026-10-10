package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/api"
)

const shapeEpsilon = 1.0e-9

type forecastPoint struct {
	ID                   int      `json:"id"`
	Threshold            *float64 `json:"threshold"`
	ThresholdDate        string   `json:"threshold_date"`
	StartingProbability  *float64 `json:"starting_probability"`
	MyProbability        *float64 `json:"my_probability"`
	CommunityProbability float64  `json:"community_probability"`
}

type probabilityUpdate struct {
	SubmarketID int
	Probability float64
	IsFixed     *bool
}

func fetchMarketShapeInfo(code string) (string, bool, error) {
	if cached, ok := getCachedMarketShape(code); ok {
		return cached.MarketType, cached.Cumulative, nil
	}

	resp, err := client.Get(api.Path("/markets/{code}", code), nil)
	if err != nil {
		return "", false, err
	}

	data, err := resp.Data()
	if err != nil {
		return "", false, err
	}

	var market struct {
		Type       string `json:"type"`
		Cumulative bool   `json:"cumulative"`
	}
	if err := json.Unmarshal(data, &market); err != nil {
		return "", false, fmt.Errorf("parsing market metadata: %w", err)
	}

	setCachedMarketShape(code, marketShapeSnapshot{
		MarketType: market.Type,
		Cumulative: market.Cumulative,
	})

	return market.Type, market.Cumulative, nil
}

// fetchFullForecastData fetches the full forecast JSON for a market.
func fetchFullForecastData(code string) (json.RawMessage, error) {
	if cached, ok := getCachedFullForecast(code); ok {
		return cached, nil
	}

	params := url.Values{}
	params.Set("include", "full")
	params.Set("limit", "0")
	params.Set("mode", "full")

	resp, err := client.Get(api.Path("/markets/{code}/forecast", code), params)
	if err != nil {
		return nil, err
	}
	data, err := resp.Data()
	if err != nil {
		return nil, err
	}

	var meta struct {
		ResponseMode string `json:"response_mode"`
	}
	if json.Unmarshal(data, &meta) == nil && meta.ResponseMode == "summary_index" {
		return nil, fmt.Errorf("received summary_index forecast; expected full data")
	}

	setCachedFullForecast(code, data)
	return data, nil
}

const countCrossGroupThresholdQuantum = 0.001

func sortedForecastGroups(forecast map[string][]forecastPoint) []string {
	groups := make([]string, 0, len(forecast))
	for group := range forecast {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func enforceDirectionalMonotonicity(
	ids []int,
	current map[int]float64,
	fixed map[int]bool,
	direction string,
) {
	if len(ids) < 2 {
		return
	}

	violates := func(prev, curr float64) bool {
		if direction == "up" {
			return curr+shapeEpsilon < prev
		}
		return curr > prev+shapeEpsilon
	}

	for iteration := 0; iteration < 16; iteration++ {
		changed := false

		for i := 1; i < len(ids); i++ {
			prevID := ids[i-1]
			currID := ids[i]
			prev := current[prevID]
			curr := current[currID]
			if !violates(prev, curr) {
				continue
			}

			if !fixed[currID] {
				current[currID] = prev
				changed = true
				continue
			}

			if !fixed[prevID] {
				current[prevID] = curr
				changed = true
			}
		}

		if !changed {
			break
		}
	}
}

func parseProbabilityUpdatesFromBodyWithDefault(
	body map[string]interface{},
	defaultFixed *bool,
) ([]probabilityUpdate, error) {
	raw, ok := body["updates"]
	if !ok {
		return nil, nil
	}
	return parseProbabilityUpdatesWithDefault(raw, defaultFixed)
}

func parseProbabilityUpdates(raw interface{}) ([]probabilityUpdate, error) {
	return parseProbabilityUpdatesWithDefault(raw, nil)
}

func parseProbabilityUpdatesWithDefault(
	raw interface{},
	defaultFixed *bool,
) ([]probabilityUpdate, error) {
	list, err := normalizeUpdatesArray(raw)
	if err != nil {
		return nil, err
	}

	updates := make([]probabilityUpdate, 0, len(list))
	for idx, item := range list {
		updateMap, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("updates[%d] must be an object", idx)
		}
		if _, legacy := updateMap["submarket_id"]; legacy {
			return nil, fmt.Errorf("updates[%d].submarket_id is no longer supported; use submarket: \"sm_<id>\"", idx)
		}
		submarketRef := updateMap["submarket"]
		id, ok := parseSubmarketRef(submarketRef)
		if !ok {
			return nil, fmt.Errorf("updates[%d].submarket must be an sm_<integer> reference", idx)
		}
		prob, ok := toFloat(updateMap["probability"])
		if !ok {
			return nil, fmt.Errorf("updates[%d].probability must be a number or decimal string", idx)
		}
		if prob < 0 || prob > 1 {
			return nil, fmt.Errorf("updates[%d].probability must be between 0 and 1", idx)
		}

		update := probabilityUpdate{
			SubmarketID: id,
			Probability: clampProb(prob),
		}
		if fixedRaw, ok := updateMap["is_fixed"]; ok {
			if fixedVal, ok := fixedRaw.(bool); ok {
				update.IsFixed = &fixedVal
			}
		}
		if fixedRaw, ok := updateMap["isFixed"]; ok && update.IsFixed == nil {
			if fixedVal, ok := fixedRaw.(bool); ok {
				update.IsFixed = &fixedVal
			}
		}

		updates = append(updates, update)
	}

	if defaultFixed != nil {
		updates = applyDefaultIsFixed(updates, *defaultFixed)
	}

	return updates, nil
}

func applyDefaultIsFixed(updates []probabilityUpdate, defaultFixed bool) []probabilityUpdate {
	for i := range updates {
		if updates[i].IsFixed != nil {
			continue
		}
		fixed := defaultFixed
		updates[i].IsFixed = &fixed
	}
	return updates
}

func normalizeUpdatesArray(raw interface{}) ([]interface{}, error) {
	switch typed := raw.(type) {
	case []interface{}:
		return typed, nil
	case []map[string]interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out, nil
	}

	value := reflect.ValueOf(raw)
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return nil, fmt.Errorf("updates must be an array")
	}

	out := make([]interface{}, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		out = append(out, value.Index(i).Interface())
	}
	return out, nil
}

// shapeAndApplyRemainder applies the multicount remainder request to updates.
// It returns the processed updates and the remainder report. If remainderRequest
// is enabled on a non-multicount market, it returns an error.
func shapeAndApplyRemainder(
	code string,
	updates []probabilityUpdate,
	usePendingBaseline bool,
	remainderRequest multicountRemainderRequest,
) ([]probabilityUpdate, multicountRemainderReport, error) {
	updates, remainderReport, err := applyMulticountRemainder(
		code,
		updates,
		usePendingBaseline,
		remainderRequest,
	)
	if err != nil {
		return nil, remainderReport, err
	}
	if remainderRequest.Enabled() && !remainderReport.IsMulticount {
		return nil, remainderReport, fmt.Errorf("--fill-remainder/--remove-remainder are only supported for multicount markets")
	}

	return updates, remainderReport, nil
}

func probabilityUpdatesToPayload(updates []probabilityUpdate) []map[string]interface{} {
	payload := make([]map[string]interface{}, 0, len(updates))
	for _, update := range updates {
		entry := map[string]interface{}{
			"submarket":   formatSubmarketRef(update.SubmarketID),
			"probability": formatProbabilityDecimal(update.Probability),
		}
		if update.IsFixed != nil {
			entry["is_fixed"] = *update.IsFixed
		}
		payload = append(payload, entry)
	}
	return payload
}

func formatProbabilityDecimal(value float64) string {
	return strconv.FormatFloat(roundProbability(value), 'f', -1, 64)
}

func clampProb(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func probToBits(p float64) float64 {
	pp := p
	if pp < 1.0e-9 {
		pp = 1.0e-9
	}
	if pp > 1.0-1.0e-9 {
		pp = 1.0 - 1.0e-9
	}
	return math.Log2(pp / (1 - pp))
}

func bitsToProb(bits float64) float64 {
	odds := math.Pow(2, bits)
	return odds / (1 + odds)
}

func toFloat(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		v, err := typed.Float64()
		return v, err == nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		v, err := strconv.ParseFloat(trimmed, 64)
		return v, err == nil
	default:
		return 0, false
	}
}
