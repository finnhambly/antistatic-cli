package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"time"
)

const shapeEpsilon = 1.0e-9

type probabilityUpdate struct {
	SubmarketID int
	Probability float64
	IsFixed     *bool
}

type shapeOptions struct {
	UsePendingBaseline bool
}

type shapeReport struct {
	InputCount  int
	OutputCount int
}

type forecastPoint struct {
	ID                   int      `json:"id"`
	Threshold            *float64 `json:"threshold"`
	ThresholdDate        string   `json:"threshold_date"`
	CommunityProbability float64  `json:"community_probability"`
}

func shapeProbabilityUpdates(
	code string,
	input []probabilityUpdate,
	opts shapeOptions,
) ([]probabilityUpdate, shapeReport, error) {
	report := shapeReport{InputCount: len(input), OutputCount: len(input)}
	if len(input) == 0 {
		return input, report, nil
	}

	marketType, cumulative, err := fetchMarketShapeInfo(code)
	if err != nil {
		return nil, report, err
	}

	forecast, err := fetchForecastPoints(code)
	if err != nil {
		return nil, report, err
	}
	if len(forecast) == 0 {
		return input, report, nil
	}

	baseline := make(map[int]float64, len(forecast))
	groupByID := make(map[int]string, len(forecast))
	thresholdByID := make(map[int]float64, len(forecast))
	thresholdDateByID := make(map[int]string, len(forecast))

	for group, points := range forecast {
		for _, point := range points {
			baseline[point.ID] = clampProb(point.CommunityProbability)
			groupByID[point.ID] = group
			if point.Threshold != nil {
				thresholdByID[point.ID] = *point.Threshold
			}
			if point.ThresholdDate != "" {
				thresholdDateByID[point.ID] = point.ThresholdDate
			}
		}
	}

	if opts.UsePendingBaseline {
		pending, err := fetchPendingEditProbabilities(code)
		if err == nil {
			for id, p := range pending {
				if _, ok := baseline[id]; ok {
					baseline[id] = clampProb(p)
				}
			}
		}
	}

	current := make(map[int]float64, len(baseline))
	for id, p := range baseline {
		current[id] = p
	}

	anchor := make(map[int]bool, len(input))
	isFixedByID := make(map[int]bool)
	unknownAnchors := make([]probabilityUpdate, 0)

	for _, update := range input {
		if _, ok := current[update.SubmarketID]; !ok {
			unknownAnchors = append(unknownAnchors, update)
			continue
		}
		current[update.SubmarketID] = clampProb(update.Probability)
		anchor[update.SubmarketID] = true
		if update.IsFixed != nil {
			isFixedByID[update.SubmarketID] = *update.IsFixed
		}
	}

	ladders := buildLadders(marketType, cumulative, forecast, thresholdDateByID)
	for _, ladder := range ladders {
		anchorIndices := make([]int, 0)
		for i, id := range ladder {
			if anchor[id] {
				anchorIndices = append(anchorIndices, i)
			}
		}
		if len(anchorIndices) == 0 {
			continue
		}

		interpolateLadder(ladder, anchorIndices, baseline, current, anchor)

		switch {
		case marketType == "count":
			enforceDirectionalMonotonicity(ladder, current, anchor, "down")
		case marketType == "date" && cumulative:
			enforceDirectionalMonotonicity(ladder, current, anchor, "up")
		}
	}

	updatesByID := make(map[int]probabilityUpdate)
	for id, base := range baseline {
		value := current[id]
		if math.Abs(value-base) <= shapeEpsilon {
			continue
		}
		updatesByID[id] = probabilityUpdate{
			SubmarketID: id,
			Probability: roundProbability(value),
		}
	}

	for _, update := range input {
		if update.IsFixed == nil {
			continue
		}
		if existing, ok := updatesByID[update.SubmarketID]; ok {
			existing.IsFixed = update.IsFixed
			updatesByID[update.SubmarketID] = existing
			continue
		}
		if value, ok := current[update.SubmarketID]; ok {
			updatesByID[update.SubmarketID] = probabilityUpdate{
				SubmarketID: update.SubmarketID,
				Probability: roundProbability(value),
				IsFixed:     update.IsFixed,
			}
			continue
		}
		updatesByID[update.SubmarketID] = update
	}

	result := make([]probabilityUpdate, 0, len(updatesByID)+len(unknownAnchors))
	for _, update := range updatesByID {
		result = append(result, update)
	}
	result = append(result, unknownAnchors...)
	sort.Slice(result, func(i, j int) bool { return result[i].SubmarketID < result[j].SubmarketID })

	report.OutputCount = len(result)
	_ = groupByID
	_ = thresholdByID
	return result, report, nil
}

func fetchMarketShapeInfo(code string) (string, bool, error) {
	resp, err := client.Get("/markets/"+code, nil)
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
	return market.Type, market.Cumulative, nil
}

func fetchForecastPoints(code string) (map[string][]forecastPoint, error) {
	params := url.Values{}
	params.Set("include", "full")
	params.Set("limit", "1000000")

	resp, err := client.Get("/markets/"+code+"/forecast", params)
	if err != nil {
		return nil, err
	}

	data, err := resp.Data()
	if err != nil {
		return nil, err
	}

	var payload struct {
		Forecast map[string][]forecastPoint `json:"forecast"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parsing forecast payload: %w", err)
	}

	return payload.Forecast, nil
}

func fetchPendingEditProbabilities(code string) (map[int]float64, error) {
	resp, err := client.Get("/markets/"+code+"/pending-edits", nil)
	if err != nil {
		return nil, err
	}

	data, err := resp.Data()
	if err != nil {
		return nil, err
	}

	var pending map[string]map[string]interface{}
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, err
	}

	out := make(map[int]float64, len(pending))
	for rawID, entry := range pending {
		id, err := strconv.Atoi(rawID)
		if err != nil {
			continue
		}
		prob, ok := toFloat(entry["probability"])
		if !ok {
			continue
		}
		out[id] = clampProb(prob)
	}
	return out, nil
}

func buildLadders(
	marketType string,
	cumulative bool,
	forecast map[string][]forecastPoint,
	thresholdDateByID map[int]string,
) [][]int {
	ladders := make([][]int, 0)

	switch marketType {
	case "count":
		groups := sortedForecastGroups(forecast)
		for _, group := range groups {
			points := append([]forecastPoint(nil), forecast[group]...)
			sort.Slice(points, func(i, j int) bool {
				li := math.Inf(1)
				if points[i].Threshold != nil {
					li = *points[i].Threshold
				}
				lj := math.Inf(1)
				if points[j].Threshold != nil {
					lj = *points[j].Threshold
				}
				if li == lj {
					return points[i].ID < points[j].ID
				}
				return li < lj
			})

			ids := make([]int, 0, len(points))
			for _, point := range points {
				ids = append(ids, point.ID)
			}
			if len(ids) > 1 {
				ladders = append(ladders, ids)
			}
		}

	case "date":
		if cumulative {
			all := make([]forecastPoint, 0)
			for _, points := range forecast {
				all = append(all, points...)
			}
			sort.Slice(all, func(i, j int) bool {
				di := all[i].ThresholdDate
				dj := all[j].ThresholdDate
				if di == dj {
					return all[i].ID < all[j].ID
				}
				return di < dj
			})
			ids := make([]int, 0, len(all))
			for _, point := range all {
				ids = append(ids, point.ID)
			}
			if len(ids) > 1 {
				ladders = append(ladders, ids)
			}
		} else {
			groups := sortedForecastGroups(forecast)
			for _, group := range groups {
				points := append([]forecastPoint(nil), forecast[group]...)
				sort.Slice(points, func(i, j int) bool {
					di := points[i].ThresholdDate
					dj := points[j].ThresholdDate
					if di == dj {
						return points[i].ID < points[j].ID
					}
					return di < dj
				})
				ids := make([]int, 0, len(points))
				for _, point := range points {
					ids = append(ids, point.ID)
				}
				if len(ids) > 1 {
					ladders = append(ladders, ids)
				}
			}
		}
	}

	_ = thresholdDateByID
	return ladders
}

func sortedForecastGroups(forecast map[string][]forecastPoint) []string {
	groups := make([]string, 0, len(forecast))
	for group := range forecast {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func interpolateLadder(
	ids []int,
	anchorIndices []int,
	baseline map[int]float64,
	current map[int]float64,
	anchor map[int]bool,
) {
	if len(ids) < 3 {
		return
	}

	boundaries := make([]int, 0, len(anchorIndices)+2)
	boundaries = append(boundaries, 0)
	boundaries = append(boundaries, anchorIndices...)
	last := len(ids) - 1
	if boundaries[len(boundaries)-1] != last {
		boundaries = append(boundaries, last)
	}
	boundaries = uniqueSortedInts(boundaries)

	for i := 0; i < len(boundaries)-1; i++ {
		left := boundaries[i]
		right := boundaries[i+1]
		if right-left <= 1 {
			continue
		}

		leftID := ids[left]
		rightID := ids[right]
		leftDelta := probToBits(current[leftID]) - probToBits(baseline[leftID])
		rightDelta := probToBits(current[rightID]) - probToBits(baseline[rightID])
		span := float64(right - left)

		for idx := left + 1; idx < right; idx++ {
			id := ids[idx]
			if anchor[id] {
				continue
			}
			fraction := float64(idx-left) / span
			delta := leftDelta + (rightDelta-leftDelta)*fraction
			current[id] = clampProb(bitsToProb(probToBits(baseline[id]) + delta))
		}
	}
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

func parseProbabilityUpdatesFromBody(body map[string]interface{}) ([]probabilityUpdate, error) {
	raw, ok := body["updates"]
	if !ok {
		return nil, nil
	}
	return parseProbabilityUpdates(raw)
}

func parseProbabilityUpdates(raw interface{}) ([]probabilityUpdate, error) {
	list, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("updates must be an array")
	}

	updates := make([]probabilityUpdate, 0, len(list))
	for _, item := range list {
		updateMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		idFloat, ok := toFloat(updateMap["submarket_id"])
		if !ok {
			continue
		}
		prob, ok := toFloat(updateMap["probability"])
		if !ok {
			continue
		}
		id := int(idFloat)
		if id <= 0 {
			continue
		}
		if prob < 0 || prob > 1 {
			continue
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
	return updates, nil
}

func probabilityUpdatesToPayload(updates []probabilityUpdate) []map[string]interface{} {
	payload := make([]map[string]interface{}, 0, len(updates))
	for _, update := range updates {
		entry := map[string]interface{}{
			"submarket_id": update.SubmarketID,
			"probability":  roundProbability(update.Probability),
		}
		if update.IsFixed != nil {
			entry["is_fixed"] = *update.IsFixed
		}
		payload = append(payload, entry)
	}
	return payload
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

func uniqueSortedInts(values []int) []int {
	if len(values) == 0 {
		return values
	}
	sort.Ints(values)
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
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
		if typed == "" {
			return 0, false
		}
		v, err := strconv.ParseFloat(typed, 64)
		return v, err == nil
	default:
		return 0, false
	}
}

func parseRFC3339(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
