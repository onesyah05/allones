package cron

import (
	"testing"
	"time"
)

func TestParseAndNext(t *testing.T) {
	s, err := Parse("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	// Saturday 2026-01-03 10:00 -> next weekday 09:00 is Monday 2026-01-05
	from := time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)
	nx := s.Next(from)
	if nx.Weekday() != time.Monday || nx.Hour() != 9 || nx.Minute() != 0 {
		t.Errorf("got %v", nx)
	}
}

func TestEvery(t *testing.T) {
	s, err := Parse("@every 15m")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Now()
	if d := s.Next(from).Sub(from); d < 14*time.Minute || d > 16*time.Minute {
		t.Errorf("got %v", d)
	}
}

func TestShorthand(t *testing.T) {
	s, err := Parse("@daily")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 6, 1, 13, 30, 0, 0, time.UTC)
	nx := s.Next(from)
	if nx.Hour() != 0 || nx.Minute() != 0 || nx.Day() != 2 {
		t.Errorf("got %v", nx)
	}
}

func TestStep(t *testing.T) {
	s, err := Parse("*/15 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)
	if nx := s.Next(from); nx.Minute() != 15 {
		t.Errorf("got %v", nx)
	}
}

func TestBad(t *testing.T) {
	for _, e := range []string{"", "1 2 3", "99 * * * *", "* * * * 9"} {
		if _, err := Parse(e); err == nil {
			t.Errorf("%q should fail", e)
		}
	}
}
