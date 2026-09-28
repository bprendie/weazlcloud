package library

import (
	"os"
	"strconv"
)

func previewLimit(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil || value <= 0 || value > 1<<50 {
		return fallback
	}
	return value
}
