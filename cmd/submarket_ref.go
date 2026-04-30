package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func formatSubmarketRef(id int) string {
	return fmt.Sprintf("sm_%d", id)
}

func parseSubmarketRef(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return positiveInt(typed)
	case int64:
		return positiveInt(int(typed))
	case float64:
		id := int(typed)
		if typed == float64(id) {
			return positiveInt(id)
		}
	case float32:
		id := int(typed)
		if typed == float32(id) {
			return positiveInt(id)
		}
	case json.Number:
		id, err := typed.Int64()
		if err == nil {
			return positiveInt(int(id))
		}
	case string:
		raw := strings.TrimSpace(typed)
		raw = strings.TrimPrefix(raw, "sm_")
		id, err := strconv.Atoi(raw)
		if err == nil {
			return positiveInt(id)
		}
	}

	return 0, false
}

func positiveInt(value int) (int, bool) {
	if value <= 0 {
		return 0, false
	}
	return value, true
}
