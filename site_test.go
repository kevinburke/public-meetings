package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateSitePerInstance(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		DataDir:       filepath.Join(tmpDir, "data"),
		SiteOutputDir: filepath.Join(tmpDir, "site"),
		Instances: []InstanceConfig{
			{
				Slug:          "walnut-creek",
				Name:          "Walnut Creek",
				Description:   "Walnut Creek meetings",
				ChannelHandle: "@WalnutCreekGov",
				AgendaRSSURL:  "https://example.com/walnut-creek.rss",
				TimeZone:      "America/Los_Angeles",
			},
			{
				Slug:          "oakland",
				Name:          "Oakland",
				Description:   "Oakland meetings",
				ChannelHandle: "@Oakland",
				AgendaRSSURL:  "https://example.com/oakland.rss",
				TimeZone:      "America/Los_Angeles",
			},
		},
	}

	db := &Database{
		Meetings: []*Meeting{
			{
				InstanceSlug: "walnut-creek",
				ID:           "2026-02-03-city-council",
				Date:         time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC),
				Body:         CityCouncil,
				Title:        "Walnut Creek City Council: 2/3/26",
				VideoURL:     "https://www.youtube.com/watch?v=abc123",
				Status:       StatusTranscribed,
			},
		},
	}

	if err := GenerateSite(cfg, db); err != nil {
		t.Fatalf("GenerateSite() error: %v", err)
	}

	mustExist := []string{
		filepath.Join(cfg.SiteOutputDir, "index.html"),
		filepath.Join(cfg.SiteOutputDir, "walnut-creek", "index.html"),
		filepath.Join(cfg.SiteOutputDir, "walnut-creek", "2026-02-03-city-council.html"),
		filepath.Join(cfg.SiteOutputDir, "oakland", "index.html"),
	}
	for _, path := range mustExist {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}
