package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseWhisperTS(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"00:00.000", 0},
		{"00:33.880", 33.88},
		{"36:20.000", 2180},
		{"59:55.420", 3595.42},
		{"01:00:02.540", 3602.54},
		{"02:15:30.000", 8130},
	}
	for _, tt := range tests {
		got := parseWhisperTS(tt.input)
		if got != tt.want {
			t.Errorf("parseWhisperTS(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSessionFromTitle(t *testing.T) {
	tests := []struct {
		title string
		body  MeetingBody
		want  string
	}{
		{"Walnut Creek City Council: 2/3/26", CityCouncil, ""},
		{"Walnut Creek City Council: Closed session - 2/2/2026", CityCouncil, "closed-session"},
		{"Walnut Creek City Council: Special meeting - 2/5/2026", CityCouncil, "special-meeting"},
		{"Walnut Creek Planning Commission: 2-12-26", PlanningCommission, ""},
		{"Walnut Creek City Council: Budget workshop - 3/1/26", CityCouncil, "budget-workshop"},
		{"No colon here 2/3/26", CityCouncil, ""},
		// Session qualifier before the colon
		{"Walnut Creek City Council Special Meeting: 1/20/2026", CityCouncil, "special-meeting"},
		{"Walnut Creek City Council Special Meeting: 9/2/2025", CityCouncil, "special-meeting"},
		{"Walnut Creek City Council Special Meeting: 12/16/2025", CityCouncil, "special-meeting"},
		// No colon, no numeric date
		{"2025 Walnut Creek City Council Holiday Greetings", CityCouncil, "holiday-greetings"},
		// Design review with session before colon
		{"Design Review Commission- Special Meeting: October 22, 2025", DesignReviewCommission, "special-meeting"},
	}
	for _, tt := range tests {
		got := sessionFromTitle(tt.title, tt.body)
		if got != tt.want {
			t.Errorf("sessionFromTitle(%q, %q) = %q, want %q", tt.title, tt.body, got, tt.want)
		}
	}
}

func TestParseVTTTimestamp(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"00:00.000", 0},
		{"00:33.880", 33*time.Second + 880*time.Millisecond},
		{"01:03.240", 1*time.Minute + 3*time.Second + 240*time.Millisecond},
		{"59:55.420", 59*time.Minute + 55*time.Second + 420*time.Millisecond},
		{"00:00:00.000", 0},
		{"00:01:03.240", 1*time.Minute + 3*time.Second + 240*time.Millisecond},
		{"01:00:02.540", 1*time.Hour + 2*time.Second + 540*time.Millisecond},
		{"02:15:30.000", 2*time.Hour + 15*time.Minute + 30*time.Second},
	}
	for _, tt := range tests {
		got, err := parseVTTTimestamp(tt.input)
		if err != nil {
			t.Errorf("parseVTTTimestamp(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseVTTTimestamp(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFetchAgendaURL(t *testing.T) {
	// Sample RSS feed matching the actual Granicus format
	const rssXML = `<?xml version="1.0" ?>
<rss version="2.0">
<channel>
  <title>City of Walnut Creek</title>
<item>
  <title>Design Review Commission Meeting  - Feb 18, 2026</title>
  <link>https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&amp;event_id=2980</link>
</item>
<item>
  <title>City Council Special Meeting 3:30pm/ Regular Meeting at 6 pm - Feb 17, 2026</title>
  <link>https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&amp;event_id=2953</link>
</item>
<item>
  <title>Planning Commission Meeting - Feb 12, 2026</title>
  <link>https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&amp;clip_id=5327</link>
</item>
<item>
  <title>City Council Special Meeting 4:30pm/ Regular Meeting at 6 pm - Feb 03, 2026</title>
  <link>https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&amp;clip_id=5323</link>
</item>
</channel>
</rss>`

	tests := []struct {
		body MeetingBody
		date string
		want string
	}{
		{
			PlanningCommission, "2026-02-12",
			"https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&clip_id=5327",
		},
		{
			CityCouncil, "2026-02-03",
			"https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&clip_id=5323",
		},
		{
			CityCouncil, "2026-02-17",
			"https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&event_id=2953",
		},
		{
			DesignReviewCommission, "2026-02-18",
			"https://walnutcreek.granicus.com/AgendaViewer.php?view_id=12&event_id=2980",
		},
		// No match
		{CityCouncil, "2026-03-01", ""},
		{PlanningCommission, "2026-02-03", ""},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, rssXML)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	origURL := granicusRSSURL
	setGranicusRSSURL(srv.URL)
	defer setGranicusRSSURL(origURL)

	for _, tt := range tests {
		date, _ := time.Parse("2006-01-02", tt.date)
		m := &Meeting{
			ID:   MeetingID(date, tt.body, ""),
			Body: tt.body,
			Date: date,
		}
		got, err := FetchAgendaURL(context.Background(), m)
		if err != nil {
			t.Errorf("FetchAgendaURL(%s, %s) error: %v", tt.body, tt.date, err)
			continue
		}
		if got != tt.want {
			t.Errorf("FetchAgendaURL(%s, %s) = %q, want %q", tt.body, tt.date, got, tt.want)
		}
	}
}

func TestParseAgendaHTML(t *testing.T) {
	// Test against the actual downloaded city council agenda
	path := filepath.Join("var", "artifacts", "2026-02-03-city-council.html")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("skipping: %s not found (run 'make setup' or download manually)", path)
	}

	items, err := ParseAgendaHTML(path)
	if err != nil {
		t.Fatalf("ParseAgendaHTML(%q) error: %v", path, err)
	}

	if len(items) == 0 {
		t.Fatal("no agenda items parsed")
	}

	// Spot-check some known items from this agenda
	found := make(map[string]string)
	for _, item := range items {
		found[item.Number] = item.Title
	}

	// The regular meeting has item 1 = OPENING and item 2 = CONSENT CALENDAR
	// (Note: the special meeting at 4:30pm also has items 1-3 before the
	// regular meeting, so we expect to see numbering restart.)
	wantItems := map[string]string{
		"2":  "CONSENT CALENDAR",
		"3":  "PUBLIC COMMUNICATIONS",
		"2a": "APPROVAL OF CITY COUNCIL MINUTES of January 20,2026.",
	}

	for num, wantSubstr := range wantItems {
		got, ok := found[num]
		if !ok {
			t.Errorf("expected item %s but not found; got items: %v", num, keys(found))
			continue
		}
		if !strings.Contains(got, wantSubstr) {
			t.Errorf("item %s = %q, want substring %q", num, got, wantSubstr)
		}
	}

	t.Logf("parsed %d agenda items", len(items))
	for _, item := range items {
		t.Logf("  %s: %.80s", item.Number, item.Title)
	}
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestParseAgendaHTMLPlanningCommission(t *testing.T) {
	path := filepath.Join("var", "artifacts", "2026-02-12-planning-commission.html")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("skipping: %s not found", path)
	}

	items, err := ParseAgendaHTML(path)
	if err != nil {
		t.Fatalf("ParseAgendaHTML(%q) error: %v", path, err)
	}

	if len(items) == 0 {
		t.Fatal("no agenda items parsed")
	}

	t.Logf("parsed %d agenda items", len(items))
	for _, item := range items {
		t.Logf("  %s: %.100s", item.Number, item.Title)
		if item.Description != "" {
			t.Logf("       desc: %.120s", item.Description)
		}
		for _, doc := range item.Documents {
			t.Logf("       doc: %s (%s)", doc.Title, doc.URL)
		}
	}

	// Check that item 4a (Mitchell Townhomes) has a description and staff report.
	for _, item := range items {
		if item.Number != "4a" {
			continue
		}
		if item.Description == "" {
			t.Error("item 4a has no description")
		}
		if !strings.Contains(item.Description, "townhouse") {
			t.Errorf("item 4a description missing 'townhouse': %.200s", item.Description)
		}
		var hasStaffReport bool
		for _, doc := range item.Documents {
			if strings.Contains(doc.Title, "Staff Report") {
				hasStaffReport = true
			}
		}
		if !hasStaffReport {
			t.Errorf("item 4a has no Staff Report document, got %d docs", len(item.Documents))
		}
	}
}

func TestCorrectTranscript(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"the Shadeland's development", "the Shadelands development"},
		{"near Shadelands Drive", "near Shadelands Drive"},
		{"Shadeland's and Shadeland's again", "Shadelands and Shadelands again"},
		{"SHADELANDS is already correct", "Shadelands is already correct"},
		{"no corrections needed here", "no corrections needed here"},
	}
	for _, tt := range tests {
		got := correctTranscript(tt.input)
		if got != tt.want {
			t.Errorf("correctTranscript(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseMeetingDate(t *testing.T) {
	fallback := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		title string
		want  string
	}{
		{"Walnut Creek Planning Commission: 2-12-26", "2026-02-12"},
		{"Walnut Creek City Council: 2/3/26", "2026-02-03"},
		{"Walnut Creek City Council: Closed session - 2/2/2026", "2026-02-02"},
	}
	for _, tt := range tests {
		got := parseMeetingDate(tt.title, fallback)
		gotStr := got.Format("2006-01-02")
		if gotStr != tt.want {
			t.Errorf("parseMeetingDate(%q) = %s, want %s", tt.title, gotStr, tt.want)
		}
	}
}
