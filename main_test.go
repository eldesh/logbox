package main

import (
	"reflect"
	"testing"
	"time"
)

func TestNewRingBufferMinCapacity(t *testing.T) {
	r := newRingBuffer(0)
	if r.capacity != 1 {
		t.Fatalf("capacity = %d, want 1", r.capacity)
	}
}

func TestRingBufferAddAndEvict(t *testing.T) {
	r := newRingBuffer(3)
	if evicted := r.add(logLine{text: "1"}); evicted {
		t.Fatal("first insert should not evict")
	}
	if evicted := r.add(logLine{text: "2"}); evicted {
		t.Fatal("second insert should not evict")
	}
	if evicted := r.add(logLine{text: "3"}); evicted {
		t.Fatal("third insert should not evict")
	}
	if evicted := r.add(logLine{text: "4"}); !evicted {
		t.Fatal("fourth insert should evict oldest")
	}

	got := r.lines()
	want := []logLine{{text: "2"}, {text: "3"}, {text: "4"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestCalcWindow(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		available int
		start     int
		follow    bool
		wantStart int
		wantEnd   int
		wantMax   int
	}{
		{
			name:      "follow moves to tail",
			total:     10,
			available: 3,
			start:     0,
			follow:    true,
			wantStart: 7,
			wantEnd:   10,
			wantMax:   7,
		},
		{
			name:      "scroll clamps start",
			total:     10,
			available: 3,
			start:     99,
			follow:    false,
			wantStart: 7,
			wantEnd:   10,
			wantMax:   7,
		},
		{
			name:      "negative available treated as zero",
			total:     10,
			available: -1,
			start:     2,
			follow:    false,
			wantStart: 2,
			wantEnd:   2,
			wantMax:   10,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd, gotMax := calcWindow(tc.total, tc.available, tc.start, tc.follow)
			if gotStart != tc.wantStart || gotEnd != tc.wantEnd || gotMax != tc.wantMax {
				t.Fatalf("calcWindow(%d,%d,%d,%t) = (%d,%d,%d), want (%d,%d,%d)",
					tc.total, tc.available, tc.start, tc.follow,
					gotStart, gotEnd, gotMax,
					tc.wantStart, tc.wantEnd, tc.wantMax,
				)
			}
		})
	}
}

func TestApplyNav(t *testing.T) {
	tests := []struct {
		name       string
		cmd        navCommand
		linesCount int
		available  int
		start      int
		follow     bool
		wantStart  int
		wantFollow bool
	}{
		{
			name:       "up from follow goes to scroll and one line up",
			cmd:        navUp,
			linesCount: 10,
			available:  3,
			start:      0,
			follow:     true,
			wantStart:  6,
			wantFollow: false,
		},
		{
			name:       "page down clamps at max",
			cmd:        navPageDown,
			linesCount: 10,
			available:  3,
			start:      6,
			follow:     false,
			wantStart:  7,
			wantFollow: false,
		},
		{
			name:       "top jumps to zero",
			cmd:        navTop,
			linesCount: 10,
			available:  3,
			start:      7,
			follow:     false,
			wantStart:  0,
			wantFollow: false,
		},
		{
			name:       "follow jumps to tail",
			cmd:        navFollow,
			linesCount: 10,
			available:  3,
			start:      0,
			follow:     false,
			wantStart:  7,
			wantFollow: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotFollow := applyNav(tc.cmd, tc.linesCount, tc.available, tc.start, tc.follow)
			if gotStart != tc.wantStart || gotFollow != tc.wantFollow {
				t.Fatalf("applyNav(...) = (%d,%t), want (%d,%t)", gotStart, gotFollow, tc.wantStart, tc.wantFollow)
			}
		})
	}
}

func TestElapsedText(t *testing.T) {
	base := time.Unix(0, 0)

	if got := elapsedText(base, base.Add(15*time.Second)); got != "15.0s" {
		t.Fatalf("elapsedText under 1m = %q, want %q", got, "15.0s")
	}

	if got := elapsedText(base, base.Add(75*time.Second)); got != "1:15" {
		t.Fatalf("elapsedText over 1m = %q, want %q", got, "1:15")
	}

	if got := elapsedText(base, base.Add(3661*time.Second)); got != "1:01:01" {
		t.Fatalf("elapsedText over 1h = %q, want %q", got, "1:01:01")
	}
}
