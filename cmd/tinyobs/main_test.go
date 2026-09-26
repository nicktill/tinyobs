package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		72 * time.Hour:   "72h",
		5 * time.Minute:  "5m",
		90 * time.Second: "1m30s",
		15 * time.Second: "15s",
		90 * time.Minute: "1h30m",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestReorder(t *testing.T) {
	got := strings.Join(reorder([]string{"snap.tinyobs", "-data", "/tmp/x"}), " ")
	if got != "-data /tmp/x snap.tinyobs" {
		t.Errorf("reorder = %q", got)
	}
	got = strings.Join(reorder([]string{"-data=/tmp/x", "snap.tinyobs"}), " ")
	if got != "-data=/tmp/x snap.tinyobs" {
		t.Errorf("reorder = %q", got)
	}
}

func TestBrowsableAndLoopback(t *testing.T) {
	cases := []struct {
		addr     string
		browse   string
		loopback bool
	}{
		{"127.0.0.1:8421", "127.0.0.1:8421", true},
		{"[::]:8421", "localhost:8421", false},
		{"0.0.0.0:8421", "localhost:8421", false},
		{"10.0.0.5:8421", "10.0.0.5:8421", false},
	}
	for _, c := range cases {
		a, _ := net.ResolveTCPAddr("tcp", c.addr)
		if got := browsable(a); got != c.browse {
			t.Errorf("browsable(%s) = %s, want %s", c.addr, got, c.browse)
		}
		if got := isLoopback(a); got != c.loopback {
			t.Errorf("isLoopback(%s) = %v", c.addr, got)
		}
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	t.Setenv("TINYOBS_RETENTION", "forever")
	if code := run(nil); code != 2 {
		t.Errorf("invalid $TINYOBS_RETENTION: exit code %d, want 2", code)
	}
	t.Setenv("TINYOBS_RETENTION", "")
	if code := run([]string{"-scrape", "ftp://x:1"}); code != 2 {
		t.Errorf("invalid -scrape: exit code %d, want 2", code)
	}
	if code := run([]string{"stray"}); code != 2 {
		t.Errorf("stray argument: exit code %d, want 2", code)
	}
}
