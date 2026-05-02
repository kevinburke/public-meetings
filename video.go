package main

import (
	"net/url"
	"path"
	"strings"
)

// Video provider identifiers used by the site generator to pick the right
// iframe embed URL and JS player shim for a meeting.
const (
	VideoProviderYouTube = "youtube"
	VideoProviderVimeo   = "vimeo"
)

// YouTubeWatchURL returns the canonical watch URL for a YouTube video id.
// Centralised so callers can't disagree about the URL format used for de-dup.
func YouTubeWatchURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + videoID
}

// VideoEmbed describes how the site template should render a meeting's video.
// Provider is one of VideoProvider*; EmbedURL is the iframe src to use.
type VideoEmbed struct {
	Provider string
	EmbedURL string
}

// VideoEmbedFor maps a meeting's stored VideoURL to the iframe embed URL and
// provider tag the site template needs. Returns ok=false when the URL is
// empty or doesn't match a supported provider — callers should hide the
// player section in that case rather than emit a broken iframe.
func VideoEmbedFor(videoURL string) (VideoEmbed, bool) {
	if videoURL == "" {
		return VideoEmbed{}, false
	}
	u, err := url.Parse(videoURL)
	if err != nil || u.Host == "" {
		return VideoEmbed{}, false
	}
	host := strings.ToLower(u.Host)
	switch {
	case host == "www.youtube.com" || host == "youtube.com" || host == "m.youtube.com":
		// Watch URLs: ?v=ID. Shorts/embed URLs already carry the id in the path.
		if id := u.Query().Get("v"); id != "" {
			return VideoEmbed{Provider: VideoProviderYouTube, EmbedURL: "https://www.youtube.com/embed/" + id}, true
		}
		if strings.HasPrefix(u.Path, "/embed/") {
			return VideoEmbed{Provider: VideoProviderYouTube, EmbedURL: videoURL}, true
		}
	case host == "youtu.be":
		id := strings.TrimPrefix(u.Path, "/")
		if id != "" {
			return VideoEmbed{Provider: VideoProviderYouTube, EmbedURL: "https://www.youtube.com/embed/" + id}, true
		}
	case host == "vimeo.com" || strings.HasSuffix(host, ".vimeo.com"):
		// Two URL shapes:
		//   vimeo.com/event/{id}        → embed at vimeo.com/event/{id}/embed
		//   vimeo.com/{id}              → embed at player.vimeo.com/video/{id}
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 2 && segs[0] == "event" {
			return VideoEmbed{Provider: VideoProviderVimeo, EmbedURL: "https://vimeo.com/event/" + segs[1] + "/embed"}, true
		}
		if len(segs) == 1 && segs[0] != "" {
			return VideoEmbed{Provider: VideoProviderVimeo, EmbedURL: "https://player.vimeo.com/video/" + path.Base(segs[0])}, true
		}
	}
	return VideoEmbed{}, false
}
