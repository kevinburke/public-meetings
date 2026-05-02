package main

import "testing"

func TestIsNoRetryError(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			name:   "vimeo future live event",
			stderr: "ERROR: [vimeo:event] 5633547: This live event is scheduled for 2026-05-04T17:00:00-07:00",
			want:   true,
		},
		{
			name:   "youtube upcoming live",
			stderr: "ERROR: [youtube] abc: This live event will begin in 30 minutes.",
			want:   true,
		},
		{
			name:   "youtube premiere",
			stderr: "ERROR: [youtube] abc: Premieres in 2 hours",
			want:   true,
		},
		{
			name:   "transient network error retries",
			stderr: "ERROR: unable to download webpage: <urlopen error timed out>",
			want:   false,
		},
		{
			name:   "empty stderr retries",
			stderr: "",
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoRetryError(tc.stderr); got != tc.want {
				t.Errorf("isNoRetryError(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}
