// Package cron parses 5-field cron expressions (plus @shorthands and @every) and
// computes next run times.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	min, hour, dom, mon, dow uint64
	every                    time.Duration
}

var shorthands = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

func Parse(expr string) (*Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("empty schedule")
	}
	if strings.HasPrefix(expr, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(expr[7:]))
		if err != nil {
			return nil, fmt.Errorf("@every: %v", err)
		}
		if d < time.Minute {
			d = time.Minute
		}
		return &Schedule{every: d}, nil
	}
	if s, ok := shorthands[expr]; ok {
		expr = s
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected 5 fields (min hour dom mon dow), got %d", len(fields))
	}
	s := &Schedule{}
	var err error
	if s.min, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("minute: %v", err)
	}
	if s.hour, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("hour: %v", err)
	}
	if s.dom, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("day-of-month: %v", err)
	}
	if s.mon, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("month: %v", err)
	}
	if s.dow, err = parseField(fields[4], 0, 6); err != nil {
		return nil, fmt.Errorf("day-of-week: %v", err)
	}
	return s, nil
}

func parseField(f string, lo, hi int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(f, ",") {
		step := 1
		rng := part
		if base, st, ok := strings.Cut(part, "/"); ok {
			rng = base
			n, err := strconv.Atoi(st)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step %q", st)
			}
			step = n
		}
		var a, b int
		switch {
		case rng == "*":
			a, b = lo, hi
		case strings.Contains(rng, "-"):
			ls, hs, _ := strings.Cut(rng, "-")
			var err error
			if a, err = strconv.Atoi(ls); err != nil {
				return 0, err
			}
			if b, err = strconv.Atoi(hs); err != nil {
				return 0, err
			}
		default:
			n, err := strconv.Atoi(rng)
			if err != nil {
				return 0, err
			}
			a, b = n, n
		}
		if a < lo || b > hi || a > b {
			return 0, fmt.Errorf("value out of range [%d-%d]", lo, hi)
		}
		for v := a; v <= b; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

// Next returns the next activation strictly after t.
func (s *Schedule) Next(t time.Time) time.Time {
	if s.every > 0 {
		return t.Add(s.every).Truncate(time.Second)
	}
	t = t.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if s.match(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

func (s *Schedule) match(t time.Time) bool {
	if s.min&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if s.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if s.mon&(1<<uint(t.Month())) == 0 {
		return false
	}
	domMatch := s.dom&(1<<uint(t.Day())) != 0
	dowMatch := s.dow&(1<<uint(t.Weekday())) != 0
	domRestricted := s.dom != fullMask(1, 31)
	dowRestricted := s.dow != fullMask(0, 6)
	// Standard cron: if both day fields are restricted, match either.
	if domRestricted && dowRestricted {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

func fullMask(lo, hi int) uint64 {
	var m uint64
	for v := lo; v <= hi; v++ {
		m |= 1 << uint(v)
	}
	return m
}

// NextRuns returns the next n activations from now.
func NextRuns(expr string, n int) ([]int64, error) {
	s, err := Parse(expr)
	if err != nil {
		return nil, err
	}
	var out []int64
	t := time.Now()
	for i := 0; i < n; i++ {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t.UnixMilli())
	}
	return out, nil
}
