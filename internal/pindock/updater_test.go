package pindock

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVersionedTag(t *testing.T) {
	tests := []struct {
		tag     string
		wantVer []int
		wantSfx string
		wantOK  bool
	}{
		{"7-alpine", []int{7}, "-alpine", true},
		{"3.12-slim", []int{3, 12}, "-slim", true},
		{"1.26.0", []int{1, 26, 0}, "", true},
		{"8", []int{8}, "", true},
		{"3.12-slim-bookworm", []int{3, 12}, "-slim-bookworm", true},
		{"latest", nil, "", false},
		{"alpine", nil, "", false},
		{"bookworm", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			parsed, ok := parseVersionedTag(tt.tag)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.wantVer, parsed.Version)
				assert.Equal(t, tt.wantSfx, parsed.Suffix)
				assert.Equal(t, tt.tag, parsed.Raw)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name string
		a, b []int
		want int
	}{
		{"equal single", []int{7}, []int{7}, 0},
		{"less single", []int{7}, []int{8}, -1},
		{"greater single", []int{8}, []int{7}, 1},
		{"equal multi", []int{1, 26}, []int{1, 26}, 0},
		{"less minor", []int{1, 25}, []int{1, 26}, -1},
		{"greater major", []int{2, 0}, []int{1, 26}, 1},
		{"different lengths padded", []int{1}, []int{1, 0}, 0},
		{"different lengths less", []int{1}, []int{1, 1}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, compareVersions(tt.a, tt.b))
		})
	}
}

func TestFindLatestTag(t *testing.T) {
	allTags := []string{
		"5-alpine", "6-alpine", "7-alpine", "8-alpine",
		"7.4-alpine", "7.4.1-alpine", "8.0-alpine", "8.0.1-alpine",
		"7", "8", "7.4", "8.0",
		"latest", "alpine", "bookworm",
	}

	tests := []struct {
		name    string
		current string
		want    string
		wantOK  bool
	}{
		{"major with suffix", "7-alpine", "8-alpine", true},
		{"major without suffix", "7", "8", true},
		{"major.minor with suffix", "7.4-alpine", "8.0-alpine", true},
		{"major.minor without suffix", "7.4", "8.0", true},
		{"major.minor.patch with suffix", "7.4.1-alpine", "8.0.1-alpine", true},
		{"already latest", "8-alpine", "", false},
		{"non-versioned", "alpine", "", false},
		{"no matching suffix", "7-bullseye", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findLatestTag(tt.current, allTags)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
