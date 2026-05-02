package main

import "testing"

func TestVideoEmbedFor(t *testing.T) {
	cases := []struct {
		in       string
		ok       bool
		provider string
		embed    string
	}{
		{"", false, "", ""},
		{"https://www.youtube.com/watch?v=abc123", true, VideoProviderYouTube, "https://www.youtube.com/embed/abc123"},
		{"https://youtu.be/abc123", true, VideoProviderYouTube, "https://www.youtube.com/embed/abc123"},
		{"https://www.youtube.com/embed/abc123", true, VideoProviderYouTube, "https://www.youtube.com/embed/abc123"},
		{"https://vimeo.com/event/5633543", true, VideoProviderVimeo, "https://vimeo.com/event/5633543/embed"},
		{"https://vimeo.com/123456789", true, VideoProviderVimeo, "https://player.vimeo.com/video/123456789"},
		{"https://example.com/video/1", false, "", ""},
		{"not-a-url", false, "", ""},
	}
	for _, tc := range cases {
		got, ok := VideoEmbedFor(tc.in)
		if ok != tc.ok {
			t.Errorf("VideoEmbedFor(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Provider != tc.provider {
			t.Errorf("VideoEmbedFor(%q) provider=%q, want %q", tc.in, got.Provider, tc.provider)
		}
		if got.EmbedURL != tc.embed {
			t.Errorf("VideoEmbedFor(%q) embed=%q, want %q", tc.in, got.EmbedURL, tc.embed)
		}
	}
}
