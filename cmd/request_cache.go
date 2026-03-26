package cmd

import (
	"encoding/json"
	"sync"
)

type marketShapeSnapshot struct {
	MarketType string
	Cumulative bool
}

var requestCache = struct {
	mu           sync.RWMutex
	fullForecast map[string]json.RawMessage
	marketShape  map[string]marketShapeSnapshot
	pendingEdits map[string]map[int]pendingEditState
}{
	fullForecast: make(map[string]json.RawMessage),
	marketShape:  make(map[string]marketShapeSnapshot),
	pendingEdits: make(map[string]map[int]pendingEditState),
}

func getCachedFullForecast(code string) (json.RawMessage, bool) {
	requestCache.mu.RLock()
	data, ok := requestCache.fullForecast[code]
	requestCache.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return append(json.RawMessage(nil), data...), true
}

func setCachedFullForecast(code string, data json.RawMessage) {
	requestCache.mu.Lock()
	requestCache.fullForecast[code] = append(json.RawMessage(nil), data...)
	requestCache.mu.Unlock()
}

func getCachedMarketShape(code string) (marketShapeSnapshot, bool) {
	requestCache.mu.RLock()
	shape, ok := requestCache.marketShape[code]
	requestCache.mu.RUnlock()
	return shape, ok
}

func setCachedMarketShape(code string, shape marketShapeSnapshot) {
	requestCache.mu.Lock()
	requestCache.marketShape[code] = shape
	requestCache.mu.Unlock()
}

func clonePendingEditStateMap(input map[int]pendingEditState) map[int]pendingEditState {
	out := make(map[int]pendingEditState, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
