package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MeetingBody represents the type of government body holding the meeting.
type MeetingBody string

const (
	CityCouncil              MeetingBody = "city-council"
	PlanningCommission       MeetingBody = "planning-commission"
	DesignReviewCommission   MeetingBody = "design-review-commission"
	TransportationCommission MeetingBody = "transportation-commission"
)

var allBodies = []MeetingBody{
	CityCouncil,
	PlanningCommission,
	DesignReviewCommission,
	TransportationCommission,
}

func (b MeetingBody) DisplayName() string {
	switch b {
	case CityCouncil:
		return "City Council"
	case PlanningCommission:
		return "Planning Commission"
	case DesignReviewCommission:
		return "Design Review Commission"
	case TransportationCommission:
		return "Transportation Commission"
	}
	return string(b)
}

// titleDateRe matches dates like "2/3/26", "2-12-26", "12/3/2026" anywhere in
// the video title. Looks for M/D/YY or M-D-YY patterns preceded by a
// non-digit (to avoid matching inside other numbers).
var titleDateRe = regexp.MustCompile(`(?:^|[^\d])(\d{1,2})[/\-](\d{1,2})[/\-](\d{2,4})`)

// parseMeetingDate extracts the actual meeting date from a video title.
// Titles look like:
//   - "Walnut Creek City Council: 2/3/26"
//   - "Walnut Creek Planning Commission: 2-12-26"
//   - "Walnut Creek City Council: Closed session - 2/2/2026"
//
// Falls back to publishedAt if the title doesn't contain a parseable date.
func parseMeetingDate(title string, publishedAt time.Time) time.Time {
	m := titleDateRe.FindStringSubmatch(title)
	if m == nil {
		return publishedAt
	}
	month, _ := strconv.Atoi(m[1])
	day, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	if year < 100 {
		year += 2000
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return publishedAt
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// classifyVideo determines the MeetingBody from a video title.
// Returns empty string if the title doesn't match any known body.
func classifyVideo(title string) MeetingBody {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "design review"):
		return DesignReviewCommission
	case strings.Contains(lower, "planning commission"):
		return PlanningCommission
	case strings.Contains(lower, "transportation commission"):
		return TransportationCommission
	case strings.Contains(lower, "city council"):
		return CityCouncil
	}
	return ""
}

// sessionFromTitle extracts a session qualifier from the video title by
// checking two positions and slugifying the result:
//
//  1. Text between the body name and the colon (e.g., "City Council Special Meeting: 1/20/2026")
//  2. Text between the colon and the date (e.g., "City Council: Closed session - 2/2/2026")
//
// Examples:
//
//	"Walnut Creek City Council: 2/3/26"                      → ""
//	"Walnut Creek City Council: Closed session - 2/2/2026"   → "closed-session"
//	"Walnut Creek City Council Special Meeting: 1/20/2026"   → "special-meeting"
//	"2025 Walnut Creek City Council Holiday Greetings"        → "holiday-greetings"
//
// Returns empty string if there's no qualifier.
func sessionFromTitle(title string, body MeetingBody) string {
	lower := strings.ToLower(title)

	// Map body to the keyword used to identify it in the title.
	var bodyKeyword string
	switch body {
	case CityCouncil:
		bodyKeyword = "city council"
	case PlanningCommission:
		bodyKeyword = "planning commission"
	case DesignReviewCommission:
		bodyKeyword = "design review commission"
	case TransportationCommission:
		bodyKeyword = "transportation commission"
	}

	// Check for session text between the body name and the first colon.
	// This handles titles like "City Council Special Meeting: 1/20/2026".
	if bodyKeyword != "" {
		if idx := strings.Index(lower, bodyKeyword); idx >= 0 {
			afterBody := title[idx+len(bodyKeyword):]
			if colonIdx := strings.Index(afterBody, ":"); colonIdx >= 0 {
				candidate := strings.TrimSpace(afterBody[:colonIdx])
				candidate = strings.TrimRight(candidate, "- ")
				if candidate != "" {
					return slugify(candidate)
				}
			} else {
				// No colon at all — check for text between body name and
				// date or end of string. Handles titles like
				// "2025 Walnut Creek City Council Holiday Greetings".
				candidate := afterBody
				if loc := titleDateRe.FindStringIndex(candidate); loc != nil {
					candidate = candidate[:loc[0]]
				}
				candidate = strings.TrimSpace(candidate)
				candidate = strings.TrimRight(candidate, "- ")
				if candidate != "" {
					return slugify(candidate)
				}
			}
		}
	}

	// Check for session text between the first colon and the date.
	// This handles "City Council: Closed session - 2/2/2026".
	_, rest, ok := strings.Cut(title, ":")
	if !ok {
		return ""
	}

	// Remove the date portion and anything after it.
	if loc := titleDateRe.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}

	rest = strings.TrimSpace(rest)
	rest = strings.TrimRight(rest, "- ")
	if rest == "" {
		return ""
	}
	return slugify(rest)
}

// slugify converts a string to a URL/filename-safe slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// MeetingStatus tracks the processing state of a meeting.
type MeetingStatus string

const (
	StatusNew          MeetingStatus = "new"
	StatusDownloading  MeetingStatus = "downloading"
	StatusDownloaded   MeetingStatus = "downloaded"
	StatusTranscribing MeetingStatus = "transcribing"
	StatusTranscribed  MeetingStatus = "transcribed"
	StatusComplete     MeetingStatus = "complete"
)

// Meeting represents a single government meeting with its associated data.
type Meeting struct {
	ID             string        `json:"id"`
	Date           time.Time     `json:"date"`
	Body           MeetingBody   `json:"body"`
	Session        string        `json:"session,omitempty"` // e.g. "closed-session"
	Title          string        `json:"title"`
	YouTubeID      string        `json:"youtube_id"`
	AgendaURL      string        `json:"agenda_url,omitempty"`
	VideoPath      string        `json:"video_path,omitempty"`
	AudioPath      string        `json:"audio_path,omitempty"`
	TranscriptPath string        `json:"transcript_path,omitempty"`
	Status         MeetingStatus `json:"status"`
	PublishedAt    time.Time     `json:"published_at"`
}

// MeetingID generates a stable ID from the date, body type, and optional
// session qualifier (e.g. "closed-session"). This ensures that a closed session
// and regular meeting on the same day for the same body get distinct IDs.
func MeetingID(date time.Time, body MeetingBody, session string) string {
	id := fmt.Sprintf("%s-%s", date.Format("2006-01-02"), body)
	if session != "" {
		id += "-" + session
	}
	return id
}

// Database holds the persistent state of all known meetings.
type Database struct {
	mu       sync.Mutex
	path     string
	Meetings []*Meeting `json:"meetings"`
}

// LoadDatabase reads the database from disk, or creates a new one if the file
// doesn't exist.
func LoadDatabase(path string) (*Database, error) {
	db := &Database{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return db, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading database: %w", err)
	}
	if err := json.Unmarshal(data, db); err != nil {
		return nil, fmt.Errorf("parsing database: %w", err)
	}
	return db, nil
}

// Save writes the database to disk.
func (db *Database) Save() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(db.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(db.path, data, 0o644)
}

// FindByYouTubeID returns the meeting with the given YouTube video ID, or nil.
func (db *Database) FindByYouTubeID(ytID string) *Meeting {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, m := range db.Meetings {
		if m.YouTubeID == ytID {
			return m
		}
	}
	return nil
}

// Add adds a meeting to the database if it doesn't already exist.
// A meeting is considered a duplicate if it has the same YouTube video ID or
// the same meeting ID (date + body + session).
// Returns true if the meeting was added (i.e. it was new).
func (db *Database) Add(m *Meeting) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, existing := range db.Meetings {
		if existing.YouTubeID == m.YouTubeID || existing.ID == m.ID {
			return false
		}
	}
	db.Meetings = append(db.Meetings, m)
	return true
}

// MeetingsWithStatus returns all meetings with the given status.
func (db *Database) MeetingsWithStatus(status MeetingStatus) []*Meeting {
	db.mu.Lock()
	defer db.mu.Unlock()
	var result []*Meeting
	for _, m := range db.Meetings {
		if m.Status == status {
			result = append(result, m)
		}
	}
	return result
}
