package textfmt

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

func WriteLabeledValue(ctx core.Context, label, value string) error {
	return WriteIndentedValue(ctx, 2, label, value)
}

func WriteIndentedValue(ctx core.Context, indent int, label, value string) error {
	if strings.TrimSpace(value) == "" {
		value = "none"
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%s%s: %s\n", strings.Repeat(" ", indent), label, value)
	return err
}

func ValueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func QuotedValueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func EnabledDisabledLabel(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
