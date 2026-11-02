package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func environmentBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if value == "" {
		return fallback, nil
	}
	switch value {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

// environmentDuration parses a duration such as "720h" or "30d" (days are
// accepted because session limits are naturally expressed in days). An empty
// value selects fallback.
func environmentDuration(name, value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback, nil
	}
	if days, ok := strings.CutSuffix(value, "d"); ok {
		count, err := strconv.Atoi(days)
		if err != nil || count <= 0 {
			return 0, fmt.Errorf("%s must be a positive duration such as 30d or 720h", name)
		}
		return time.Duration(count) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 30d or 720h", name)
	}
	return duration, nil
}
