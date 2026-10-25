package runtimeresources

import (
	"fmt"
	"math"
)

// ScaleMilliToWholeCores multiplies milli and rounds up to a whole core; sampled
// intervals do not support fractional precision.
func ScaleMilliToWholeCores(milli int64, multiple float64) int64 {
	scaled := int64(math.Ceil(float64(milli) * multiple))
	cores := (scaled + 999) / 1000
	if cores < 1 {
		cores = 1
	}
	return cores * 1000
}

func FormatBytesAsMi(bytes int64) string {
	return fmt.Sprintf("%dMi", (bytes+1024*1024-1)/(1024*1024))
}

func RuntimeQuotaMilli(quotaCores float64) int64 {
	return int64(math.Round(quotaCores * 1000))
}
