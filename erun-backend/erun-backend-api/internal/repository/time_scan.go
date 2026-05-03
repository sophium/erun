package repository

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

type dbTime struct {
	target *time.Time
}

func scanTime(target *time.Time) dbTime {
	return dbTime{target: target}
}

func (s dbTime) Scan(value any) error {
	if s.target == nil {
		return fmt.Errorf("scan time: target is nil")
	}
	if value == nil {
		*s.target = time.Time{}
		return nil
	}
	switch typed := value.(type) {
	case time.Time:
		*s.target = typed
		return nil
	case string:
		return s.scanString(typed)
	case []byte:
		return s.scanString(string(typed))
	default:
		return fmt.Errorf("scan time: unsupported value type %T", value)
	}
}

func (s dbTime) scanString(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		*s.target = time.Time{}
		return nil
	}
	for _, layout := range dbTimeLayouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			*s.target = parsed
			return nil
		}
	}
	return fmt.Errorf("scan time: parse %q", value)
}

func (s dbTime) Value() (driver.Value, error) {
	if s.target == nil || s.target.IsZero() {
		return nil, nil
	}
	return *s.target, nil
}

var dbTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
}
