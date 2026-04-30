package cmd

import (
	"fmt"
	"strconv"
	"strings"
)

func formatSubmarketRef(id int) string {
	return fmt.Sprintf("sm_%d", id)
}

func parseSubmarketRef(value interface{}) (int, bool) {
	raw, ok := value.(string)
	if !ok {
		return 0, false
	}

	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "sm_") {
		return 0, false
	}

	id, err := strconv.Atoi(strings.TrimPrefix(raw, "sm_"))
	if err != nil {
		return 0, false
	}
	return positiveInt(id)
}

func positiveInt(value int) (int, bool) {
	if value <= 0 {
		return 0, false
	}
	return value, true
}
