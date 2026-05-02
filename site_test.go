package main

import (
	"os"
	"path/filepath"
	"strings"
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

// TestIndexLinksAllBodyTypes locks in that the per-instance index page lists
// every body present in the database, regardless of whether the code carries
// a hardcoded list of "known" bodies. A regression here means a new
// MeetingBody constant gets meeting pages generated but no link from the
// index — exactly how SchoolBoard initially shipped.
func TestIndexLinksAllBodyTypes(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		DataDir:       filepath.Join(tmpDir, "data"),
		SiteOutputDir: filepath.Join(tmpDir, "site"),
		Instances: []InstanceConfig{
			{
				Slug:          "wcsd",
				Name:          "WCSD",
				Description:   "WCSD meetings",
				Source:        SourceHighbond,
				PortalBaseURL: "https://wcsd.example.com",
				TimeZone:      "America/Los_Angeles",
			},
		},
	}
	db := &Database{
		Meetings: []*Meeting{
			{
				InstanceSlug: "wcsd",
				ID:           "2026-04-13-school-board",
				Date:         time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
				Body:         SchoolBoard,
				Title:        "WCSD Regular Governing Board Meeting - Apr 13 2026",
				VideoURL:     "https://vimeo.com/event/5633543",
				Status:       StatusComplete,
			},
		},
	}
	if err := GenerateSite(cfg, db); err != nil {
		t.Fatalf("GenerateSite: %v", err)
	}
	indexPath := filepath.Join(cfg.SiteOutputDir, "wcsd", "index.html")
	body, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(body), `href="2026-04-13-school-board.html"`) {
		t.Errorf("index page does not link the school-board meeting:\n%s", body)
	}
	if !strings.Contains(string(body), SchoolBoard.DisplayName()) {
		t.Errorf("index page missing the SchoolBoard display name %q", SchoolBoard.DisplayName())
	}
}
