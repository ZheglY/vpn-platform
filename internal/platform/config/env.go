package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type FieldError struct {
	Name string
	Err  error
}

type Error struct {
	Fields []FieldError
}

func (e *Error) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return ""
	}
	names := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		names = append(names, field.Name)
	}
	return "invalid configuration: " + strings.Join(names, ", ")
}

func (e *Error) Unwrap() error {
	if e == nil || len(e.Fields) == 0 {
		return nil
	}
	errs := make([]error, 0, len(e.Fields))
	for _, field := range e.Fields {
		errs = append(errs, fmt.Errorf("%s: %w", field.Name, field.Err))
	}
	return errors.Join(errs...)
}

func Append(errs []FieldError, name string, err error) []FieldError {
	if err == nil {
		return errs
	}
	return append(errs, FieldError{Name: name, Err: err})
}

func Combine(fields []FieldError) error {
	if len(fields) == 0 {
		return nil
	}
	return &Error{Fields: fields}
}

func String(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func RequiredString(name string) (string, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("required environment variable is missing")
	}
	return value, nil
}

func Duration(name string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse duration: %w", err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return parsed, nil
}

func Int(name string, fallback int) (int, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse int: %w", err)
	}
	return parsed, nil
}

func Bool(name string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse bool: %w", err)
	}
	return parsed, nil
}
