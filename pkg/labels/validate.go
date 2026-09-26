package labels

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits on a single series. They keep one bad client from exhausting memory
// through huge label sets.
const (
	MaxLabels      = 32
	MaxNameLength  = 128
	MaxValueLength = 1024
)

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("invalid series")

// IsValidMetricName reports whether s is a legal metric name:
// [a-zA-Z_:][a-zA-Z0-9_:]*
func IsValidMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// IsValidLabelName reports whether s is a legal label name:
// [a-zA-Z_][a-zA-Z0-9_]*
func IsValidLabelName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// Validate checks that ls is a well-formed series identity: sorted, unique
// names, a valid metric name, valid label names, no reserved names other than
// __name__, non-empty UTF-8 values, and within the size limits.
func Validate(ls Labels) error {
	if len(ls) > MaxLabels {
		return fmt.Errorf("%w: %d labels exceeds limit of %d", ErrInvalid, len(ls), MaxLabels)
	}
	if !IsValidMetricName(ls.Get(MetricName)) {
		return fmt.Errorf("%w: invalid or missing metric name %q", ErrInvalid, ls.Get(MetricName))
	}
	for i, l := range ls {
		if i > 0 && ls[i-1].Name >= l.Name {
			return fmt.Errorf("%w: labels not sorted or duplicated at %q", ErrInvalid, l.Name)
		}
		if l.Name != MetricName {
			if !IsValidLabelName(l.Name) || len(l.Name) > MaxNameLength {
				return fmt.Errorf("%w: invalid label name %q", ErrInvalid, l.Name)
			}
			if strings.HasPrefix(l.Name, "__") {
				return fmt.Errorf("%w: label name %q is reserved", ErrInvalid, l.Name)
			}
		}
		if l.Value == "" || len(l.Value) > MaxValueLength || !utf8.ValidString(l.Value) {
			return fmt.Errorf("%w: label %q has an empty, oversized or non-UTF-8 value", ErrInvalid, l.Name)
		}
	}
	return nil
}
