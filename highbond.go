package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kevinburke/public-meetings/internal/version"
	"golang.org/x/net/html"
)

// highbondClient is a thin HTTP wrapper for fetching pages off a Diligent
// Community / iCompass portal. The portal speaks plain HTML on the listing
// pages we care about, so no auth or session juggling is required.
type highbondClient struct {
	baseURL    string
	httpClient *http.Client
}

func newHighbondClient(baseURL string) *highbondClient {
	return &highbondClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *highbondClient) get(ctx context.Context, path string) ([]byte, error) {
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "public-meetings/"+version.Version)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// highbondListing is one meeting as it appears on the MeetingTypeList page.
// We only collect what's cheap to derive without a per-meeting fetch — the
// rest (agenda PDF URL, Vimeo URL) is filled in lazily during discovery.
type highbondListing struct {
	TypeName    string // meeting-type heading, e.g. "Regular Governing Board Meeting"
	Title       string // full title incl. date suffix
	MeetingPath string // /Portal/MeetingInformation.aspx?Id=N
}

// listingTitleDateRe extracts a "MMM DD YYYY" date from titles like
//
//	"Walnut Creek School District Regular Governing Board Meeting - Apr 13 2026"
//	"CANCELLED - WCSD Governing Board Meeting - CLOSED SESSION - Sep 08 2025"
//
// Falls back to the parser leaving the date empty if no match.
var listingTitleDateRe = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+(\d{1,2})[,]?\s+(\d{4})\b`)

var monthByPrefix = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September,
	"oct": time.October, "nov": time.November, "dec": time.December,
}

func parseHighbondTitleDate(title string) (time.Time, bool) {
	m := listingTitleDateRe.FindStringSubmatch(title)
	if m == nil {
		return time.Time{}, false
	}
	month, ok := monthByPrefix[strings.ToLower(m[1][:3])]
	if !ok {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	if day < 1 || day > 31 || year < 1900 {
		return time.Time{}, false
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), true
}

// classifyHighbondMeeting maps the meeting-type heading and full title to a
// (body, session) pair. WCSD only emits SchoolBoard meetings; the session
// qualifier captures the meeting flavor so MeetingID stays unique across
// regular/closed/special meetings on the same date.
func classifyHighbondMeeting(typeName, title string) (MeetingBody, string) {
	combined := strings.ToLower(typeName + " " + title)
	switch {
	case strings.Contains(combined, "closed session"):
		return SchoolBoard, "closed-session"
	case strings.Contains(combined, "strategic planning"):
		return SchoolBoard, "strategic-planning"
	case strings.Contains(combined, "special meeting"), strings.Contains(combined, "special"):
		// "Special" alone is ambiguous, but on this portal it's the
		// next most common qualifier after closed-session.
		if strings.Contains(combined, "regular") {
			// Title has both — prefer regular.
			return SchoolBoard, ""
		}
		return SchoolBoard, "special-meeting"
	case strings.Contains(combined, "notice of"),
		strings.Contains(combined, "public hearing"),
		strings.Contains(combined, "developer fee"),
		strings.Contains(combined, "textbook adoption"),
		strings.Contains(combined, "board quorum"):
		// Notice-only events — no recording; we'll filter them out
		// of discovery rather than create empty meetings.
		return "", ""
	}
	return SchoolBoard, ""
}

// parseHighbondListings walks a MeetingTypeList HTML page and returns the
// flat list of (type, title, meeting URL) tuples found in the repeater.
//
// The MeetingTypeList page renders each type as a heading (<a> or <span>
// with class meeting-type-item-title) followed by a sibling <ol> of meeting
// links. Heading and list are siblings, not parent/child, so the walker
// uses the depth-first traversal order to remember the most recently seen
// heading and attach it to subsequent meeting links.
func parseHighbondListings(body []byte) ([]highbondListing, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing MeetingTypeList HTML: %w", err)
	}

	var out []highbondListing
	var currentType string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if classContains(n, "meeting-type-item-title") {
				currentType = collapseWhitespace(textContent(n))
			}
			if n.Data == "a" && classContains(n, "list-link") {
				href := getAttr(n, "href")
				title := getAttr(n, "title")
				if title == "" {
					title = collapseWhitespace(textContent(n))
				}
				if href != "" && strings.Contains(href, "MeetingInformation.aspx") {
					out = append(out, highbondListing{
						TypeName:    currentType,
						Title:       title,
						MeetingPath: href,
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

func classContains(n *html.Node, want string) bool {
	for _, a := range n.Attr {
		if a.Key != "class" {
			continue
		}
		for c := range strings.FieldsSeq(a.Val) {
			if c == want {
				return true
			}
		}
	}
	return false
}

// extractAgendaPDFURL parses a MeetingInformation page and returns a URL
// that downloads the agenda as PDF, or "" if no agenda doc is linked.
//
// The page renders an <a id="ctl00_MainContent_DocumentPrintVersion" ...>
// pointing at the original document (typically a .docx with a `handle=`
// query token). Appending `printPdf=true` makes the same endpoint serve a
// PDF, which is what we want for both viewing and pdftohtml-based item
// extraction downstream.
func extractAgendaPDFURL(meetingHTML []byte) string {
	doc, err := html.Parse(bytes.NewReader(meetingHTML))
	if err != nil {
		return ""
	}
	var href string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if href != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			if getAttr(n, "id") == "ctl00_MainContent_DocumentPrintVersion" {
				href = getAttr(n, "href")
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if href == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(href, "http://"); ok {
		href = "https://" + rest
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("printPdf", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

// vimeoLinkRe matches Vimeo URLs in pdftohtml output. Pulled out so tests
// can exercise extractVimeoURL with synthetic input.
var vimeoLinkRe = regexp.MustCompile(`https?://(?:www\.)?vimeo\.com/(?:event/[0-9]+(?:/[A-Za-z0-9]+)?|[0-9]+)`)

// extractVimeoURL shells out to pdftohtml -i -stdout -hidden and greps the
// emitted HTML for the first vimeo.com link. Returns "" if pdftohtml is not
// installed or no link is found.
func extractVimeoURL(ctx context.Context, pdfPath string) (string, error) {
	if _, err := exec.LookPath("pdftohtml"); err != nil {
		return "", fmt.Errorf("pdftohtml not installed (poppler-utils): %w", err)
	}
	cmd := exec.CommandContext(ctx, "pdftohtml", "-i", "-stdout", "-hidden", pdfPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftohtml failed: %w (%s)", err, stderr.String())
	}
	match := vimeoLinkRe.Find(out)
	if match == nil {
		return "", nil
	}
	return string(match), nil
}

// downloadHighbondAgenda saves the agenda PDF for a Highbond meeting to disk.
// AgendaURL is expected to already be set on the meeting (populated at
// discovery time); if it's empty we treat that as "no agenda available".
func downloadHighbondAgenda(ctx context.Context, meeting *Meeting) error {
	if meeting.AgendaURL == "" {
		slog.Info("no agenda URL for highbond meeting, skipping", "meeting", meeting.ID)
		return nil
	}
	artifactDir := artifactsDir(meeting.InstanceSlug)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return fmt.Errorf("creating artifacts directory: %w", err)
	}
	pdfPath := agendaPDFPath(meeting)
	if _, err := os.Stat(pdfPath); err == nil {
		// Already downloaded; trust the on-disk copy.
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := downloadFile(ctx, meeting.AgendaURL, pdfPath); err != nil {
		return fmt.Errorf("downloading agenda PDF: %w", err)
	}
	slog.Info("downloaded highbond agenda", "meeting", meeting.ID, "path", pdfPath)
	return nil
}

// discoverHighbond is the MeetingSource implementation for Diligent Community
// portals. It reads the MeetingTypeList page, filters by configured type
// substrings (or accepts all types if none are configured), and for each
// previously-unknown meeting fetches the meeting page, downloads the agenda
// PDF, and extracts a Vimeo URL from it. Meetings without a Vimeo URL are
// skipped — they'll be retried on the next discovery pass.
func discoverHighbond(ctx context.Context, cfg *Config, inst *InstanceConfig, db *Database, _ time.Time) ([]*Meeting, error) {
	if inst.PortalBaseURL == "" {
		return nil, fmt.Errorf("instance %q: portal_base_url is required for source %q", inst.Slug, SourceHighbond)
	}
	client := newHighbondClient(inst.PortalBaseURL)

	listingHTML, err := client.get(ctx, "/Portal/MeetingTypeList.aspx")
	if err != nil {
		return nil, fmt.Errorf("fetching MeetingTypeList: %w", err)
	}
	listings, err := parseHighbondListings(listingHTML)
	if err != nil {
		return nil, err
	}
	slog.Info("highbond MeetingTypeList parsed", "instance", inst.Slug, "listings", len(listings))

	loc, err := time.LoadLocation(inst.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("loading timezone %q: %w", inst.TimeZone, err)
	}
	_ = loc // future: convert publish times for Vimeo events

	typeFilters := lowerSlice(inst.MeetingTypes)

	var newMeetings []*Meeting
	for _, lst := range listings {
		if !typeMatches(lst.TypeName, typeFilters) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lst.Title)), "cancelled") {
			continue
		}
		body, session := classifyHighbondMeeting(lst.TypeName, lst.Title)
		if body == "" {
			slog.Debug("skipping non-meeting type", "type", lst.TypeName, "title", lst.Title)
			continue
		}
		date, ok := parseHighbondTitleDate(lst.Title)
		if !ok {
			slog.Warn("could not parse date from highbond title", "title", lst.Title)
			continue
		}

		meetingID := MeetingID(date, body, session)
		// Cheap dedup: if we already have a matching ID for this
		// instance, skip the network calls.
		if existing := findMeetingByQualified(db, inst.Slug, meetingID); existing != nil {
			continue
		}

		meetingURL := absoluteURL(inst.PortalBaseURL, lst.MeetingPath)
		meetingPage, err := client.get(ctx, lst.MeetingPath)
		if err != nil {
			slog.Warn("fetching highbond meeting page", "meeting", meetingID, "error", err)
			continue
		}
		agendaURL := extractAgendaPDFURL(meetingPage)
		if agendaURL == "" {
			slog.Info("highbond meeting has no agenda yet, skipping", "meeting", meetingID, "page", meetingURL)
			continue
		}

		// Pull the PDF down once so we can extract the Vimeo URL.
		// Reuse it as the cached agenda artifact later.
		artifactDir := artifactsDir(inst.Slug)
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating artifacts directory: %w", err)
		}
		pdfPath := filepath.Join(artifactDir, meetingID+".pdf")
		if err := downloadFile(ctx, agendaURL, pdfPath); err != nil {
			slog.Warn("downloading highbond agenda PDF", "meeting", meetingID, "error", err)
			continue
		}
		videoURL, err := extractVimeoURL(ctx, pdfPath)
		if err != nil {
			slog.Warn("extracting vimeo URL from agenda", "meeting", meetingID, "error", err)
			continue
		}
		if videoURL == "" {
			slog.Info("agenda has no vimeo link, skipping", "meeting", meetingID, "agenda", agendaURL)
			// Drop the PDF too — we'll re-download next pass when
			// the agenda is updated with a stream link.
			os.Remove(pdfPath)
			continue
		}

		m := &Meeting{
			InstanceSlug: inst.Slug,
			ID:           meetingID,
			Date:         date,
			Body:         body,
			Session:      session,
			Title:        lst.Title,
			VideoURL:     videoURL,
			AgendaURL:    agendaURL,
			Status:       StatusNew,
			PublishedAt:  date, // approximate — Highbond doesn't expose publish times
		}
		if db.Add(m) {
			newMeetings = append(newMeetings, m)
			slog.Info("found new highbond meeting", "instance", inst.Slug, "title", m.Title, "video_url", m.VideoURL, "agenda_url", m.AgendaURL)
		}
	}
	return newMeetings, nil
}

// absoluteURL joins a portal base URL with a relative path lifted from a
// scraped anchor.
func absoluteURL(base, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// findMeetingByQualified is the read-side dedup helper for discoverHighbond.
// We can't use db.FindByVideoURL because we haven't computed the video URL
// yet at the point we want to skip a known meeting.
func findMeetingByQualified(db *Database, slug, id string) *Meeting {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, m := range db.Meetings {
		if m.InstanceSlug == slug && m.ID == id {
			return m
		}
	}
	return nil
}

func lowerSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

// typeMatches reports whether the given meeting-type heading should be
// included in discovery. Empty filters means accept all types.
func typeMatches(typeName string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	low := strings.ToLower(typeName)
	for _, f := range filters {
		if strings.Contains(low, f) {
			return true
		}
	}
	return false
}
