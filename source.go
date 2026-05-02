package main

import (
	"context"
	"fmt"
	"time"
)

// Meeting source identifiers. Each instance's `source` field selects which
// discovery backend is used to find new meetings on the configured channel.
const (
	SourceYouTube  = "youtube"
	SourceHighbond = "highbond"
)

// DiscoverMeetings finds new meetings for the given instance and adds them
// to the database. The discovery backend is chosen by inst.Source.
//
// `since` is a hint for backends that support time-range queries (YouTube);
// backends that scrape a small fixed listing (Highbond) may ignore it.
func DiscoverMeetings(ctx context.Context, cfg *Config, inst *InstanceConfig, db *Database, since time.Time) ([]*Meeting, error) {
	switch inst.Source {
	case "", SourceYouTube:
		return discoverYouTube(ctx, cfg, inst, db, since)
	case SourceHighbond:
		return discoverHighbond(ctx, cfg, inst, db, since)
	default:
		return nil, fmt.Errorf("instance %q: unknown source %q", inst.Slug, inst.Source)
	}
}

// discoverYouTube wraps the existing YouTube discovery flow so all of its
// per-call setup (channel handle resolution, client construction) lives in one
// place behind DiscoverMeetings.
func discoverYouTube(ctx context.Context, cfg *Config, inst *InstanceConfig, db *Database, since time.Time) ([]*Meeting, error) {
	if cfg.YouTubeAPIKey == "" {
		return nil, fmt.Errorf("instance %q uses youtube source but youtube_api_key is not configured", inst.Slug)
	}
	yt := NewYouTubeClient(cfg.YouTubeAPIKey)
	channelID, err := yt.ResolveChannelID(ctx, inst.ChannelHandle)
	if err != nil {
		return nil, fmt.Errorf("resolving channel for %s: %w", inst.Slug, err)
	}
	return CheckForNewMeetings(ctx, yt, inst, channelID, db, since)
}
