package config

import (
	"fmt"
	"math"
)

// ScaleMilliToWholeCores multiplies a millicore figure and rounds up to a whole
// core. Every environment in the fleet is sized in whole cores, and a
// fractional suggestion derived from sampled intervals claims a precision the
// samples do not have.
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

// RuntimeQuotaMilli converts RuntimeCPUUsage's cores-based quota into the
// millicore unit every sizing figure and NamespaceResourceQuota comparison in
// this file uses.
func RuntimeQuotaMilli(quotaCores float64) int64 {
	return int64(math.Round(quotaCores * 1000))
}
