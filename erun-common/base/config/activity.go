package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEnvironmentIdleTimeout      = 5 * time.Minute
	DefaultEnvironmentWorkingHours     = "08:00-20:00"
	DefaultEnvironmentIdleTrafficBytes = 0

	ActivityKindSSH   = "ssh"
	ActivityKindAPI   = "api"
	ActivityKindMCP   = "mcp"
	ActivityKindCLI   = "cli"
	ActivityKindCodex = "codex"
)

type EnvironmentIdleConfig struct {
	Timeout          string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	WorkingHours     string `yaml:"workinghours,omitempty" json:"workingHours,omitempty"`
	Timezone         string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	IdleTrafficBytes int64  `yaml:"idletrafficbytes,omitempty" json:"idleTrafficBytes,omitempty"`
}

type EnvironmentIdlePolicy struct {
	Timeout          time.Duration `json:"timeout"`
	WorkingHours     string        `json:"workingHours"`
	Timezone         string        `json:"timezone,omitempty"`
	IdleTrafficBytes int64         `json:"idleTrafficBytes"`
}

func (c EnvironmentIdleConfig) Resolve() (EnvironmentIdlePolicy, error) {
	timeout := strings.TrimSpace(c.Timeout)
	if timeout == "" {
		timeout = DefaultEnvironmentIdleTimeout.String()
	}
	duration, err := time.ParseDuration(timeout)
	if err != nil {
		return EnvironmentIdlePolicy{}, fmt.Errorf("invalid environment idle timeout %q", timeout)
	}
	if duration <= 0 {
		return EnvironmentIdlePolicy{}, fmt.Errorf("environment idle timeout must be greater than zero")
	}

	workingHours := strings.TrimSpace(c.WorkingHours)
	if workingHours == "" {
		workingHours = DefaultEnvironmentWorkingHours
	}
	if err := validateWorkingHours(workingHours); err != nil {
		return EnvironmentIdlePolicy{}, err
	}

	timezone := strings.TrimSpace(c.Timezone)
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return EnvironmentIdlePolicy{}, fmt.Errorf("invalid environment idle timezone %q: %w", timezone, err)
		}
	}

	idleTrafficBytes := c.IdleTrafficBytes
	if idleTrafficBytes < 0 {
		return EnvironmentIdlePolicy{}, fmt.Errorf("environment idle traffic threshold must not be negative")
	}

	return EnvironmentIdlePolicy{
		Timeout:          duration,
		WorkingHours:     workingHours,
		Timezone:         timezone,
		IdleTrafficBytes: idleTrafficBytes,
	}, nil
}

func validateWorkingHours(value string) error {
	start, end, err := ParseWorkingHours(value)
	if err != nil {
		return err
	}
	if start == end {
		return fmt.Errorf("environment working hours start and end must differ")
	}
	return nil
}

func ParseWorkingHours(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("environment working hours must use HH:MM-HH:MM")
	}
	start, err := parseClockMinute(parts[0])
	if err != nil {
		return 0, 0, err
	}
	end, err := parseClockMinute(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

func parseClockMinute(value string) (int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("environment working hours must use HH:MM-HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid environment working hour %q", value)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid environment working minute %q", value)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("environment working hours must use valid 24-hour times")
	}
	return hour*60 + minute, nil
}
