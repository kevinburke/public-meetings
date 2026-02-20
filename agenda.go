package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const granicusBase = "https://walnutcreek.granicus.com"

// granicusRSSURL is the RSS feed for all agenda documents across all bodies.
var granicusRSSURL = granicusBase + "/ViewPublisherRSS.php?view_id=12&mode=agendas"

// setGranicusRSSURL overrides the RSS URL (for testing).
func setGranicusRSSURL(u string) { granicusRSSURL = u }

// rssItem represents a single item in the Granicus RSS feed.
type rssItem struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
}

type rssFeed struct {
	Items []rssItem `xml:"channel>item"`
}

// bodyKeywords maps MeetingBody to keywords to look for in RSS item titles.
var bodyKeywords = map[MeetingBody]string{
	CityCouncil:              "city council",
	PlanningCommission:       "planning commission",
	DesignReviewCommission:   "design review commission",
	TransportationCommission: "transportation commission",
}

// AgendaDocument is a link to a document attached to an agenda item.
type AgendaDocument struct {
	Title string // e.g. "Staff Report", "Attachment 1 - CEQA Resolution"
	URL   string
}

// AgendaItem represents a single item parsed from a Granicus agenda page.
type AgendaItem struct {
	Number      string           // e.g. "1", "2", "2a", "4a"
	Title       string           // text of the agenda item
	Description string           // body text (project description, CEQA info, staff contact, etc.)
	Documents   []AgendaDocument // attached documents (staff report, attachments)
}

// FetchAgendaURL fetches the Granicus RSS feed and finds the agenda URL
// matching the meeting's body and date.
func FetchAgendaURL(ctx context.Context, meeting *Meeting) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", granicusRSSURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching Granicus RSS feed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", fmt.Errorf("parsing Granicus RSS feed: %w", err)
	}

	keyword, ok := bodyKeywords[meeting.Body]
	if !ok {
		return "", fmt.Errorf("no keyword mapping for body %q", meeting.Body)
	}

	// Format the date as it appears in RSS titles: "Feb 03, 2026"
	dateStr := meeting.Date.Format("Jan 02, 2006")

	for _, item := range feed.Items {
		lower := strings.ToLower(item.Title)
		if strings.Contains(lower, keyword) && strings.Contains(item.Title, dateStr) {
			return item.Link, nil
		}
	}

	slog.Warn("no agenda found in RSS feed",
		"meeting", meeting.ID,
		"body", meeting.Body,
		"date", dateStr,
	)
	return "", nil
}

// DownloadAgenda finds the agenda URL, downloads the HTML to var/artifacts/,
// parses the agenda items, and stores the URL on the meeting.
func DownloadAgenda(ctx context.Context, cfg *Config, meeting *Meeting) error {
	agendaURL, err := FetchAgendaURL(ctx, meeting)
	if err != nil {
		return err
	}
	if agendaURL == "" {
		slog.Info("no agenda found, skipping", "meeting", meeting.ID)
		return nil
	}

	meeting.AgendaURL = agendaURL

	// Download agenda HTML to var/artifacts/
	artifactsDir := filepath.Join(projectRoot(), "var", "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return fmt.Errorf("creating artifacts directory: %w", err)
	}

	htmlPath := filepath.Join(artifactsDir, meeting.ID+".html")
	if err := downloadFile(ctx, agendaURL, htmlPath); err != nil {
		slog.Warn("could not download agenda HTML", "meeting", meeting.ID, "error", err)
		// Non-fatal: we still have the URL
	} else {
		slog.Info("downloaded agenda", "meeting", meeting.ID, "path", htmlPath)
	}

	return nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// agendaNumberRe matches item numbers like "1.", "2.", "a.", "b." in the
// width=20 cells of Granicus agenda HTML.
var agendaNumberRe = regexp.MustCompile(`^([0-9]+|[a-z])\.?$`)

// ParseAgendaHTML parses agenda items from a downloaded Granicus agenda HTML
// file. It looks for the characteristic pattern of <td width=20> cells
// containing item numbers adjacent to <td> cells containing item text.
// It also collects description paragraphs and document links that follow
// each numbered item.
func ParseAgendaHTML(path string) ([]AgendaItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	doc, err := html.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	// Flatten the DOM into a sequence of interesting nodes: numbered items,
	// description cells, and document links.
	type nodeKind int
	const (
		kindNumbered nodeKind = iota // td width=20 with a valid number
		kindDesc                     // td width=20 empty (continuation text in sibling td)
		kindDoc                      // <a class="Document ...">
	)
	type entry struct {
		kind   nodeKind
		number string // for kindNumbered
		text   string // title/description text
		url    string // for kindDoc
	}

	var entries []entry
	var lastTopLevel string

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "td" && hasAttr(n, "width", "20") {
			number := strings.TrimSpace(textContent(n))
			next := nextElementSibling(n)

			if number != "" && agendaNumberRe.MatchString(number) {
				// Numbered item
				if next == nil || next.Data != "td" {
					goto recurse
				}
				text := collapseWhitespace(textContent(next))
				if text == "" {
					goto recurse
				}
				number = strings.TrimSuffix(number, ".")
				if number[0] >= 'a' && number[0] <= 'z' {
					number = lastTopLevel + number
				} else {
					lastTopLevel = number
				}
				entries = append(entries, entry{kind: kindNumbered, number: number, text: text})
			} else if number == "" && next != nil && next.Data == "td" {
				// Empty number cell = continuation/description text
				text := collapseWhitespace(textContent(next))
				if text != "" {
					entries = append(entries, entry{kind: kindDesc, text: text})
				}
			}
			// Don't recurse into children of this td — we already consumed the row.
			return
		}

		if n.Type == html.ElementNode && n.Data == "a" && hasAttrContains(n, "class", "Document") {
			href := getAttr(n, "href")
			text := collapseWhitespace(textContent(n))
			if href != "" && text != "" {
				entries = append(entries, entry{kind: kindDoc, text: text, url: href})
			}
			return
		}

	recurse:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// Group entries: each kindNumbered starts a new item; subsequent
	// kindDesc and kindDoc entries belong to that item.
	var items []AgendaItem
	for _, e := range entries {
		switch e.kind {
		case kindNumbered:
			items = append(items, AgendaItem{
				Number: e.number,
				Title:  e.text,
			})
		case kindDesc:
			if len(items) > 0 {
				last := &items[len(items)-1]
				if last.Description != "" {
					last.Description += "\n\n"
				}
				last.Description += e.text
			}
		case kindDoc:
			if len(items) > 0 {
				last := &items[len(items)-1]
				last.Documents = append(last.Documents, AgendaDocument{
					Title: e.text,
					URL:   e.url,
				})
			}
		}
	}

	return items, nil
}

func hasAttr(n *html.Node, key, val string) bool {
	for _, a := range n.Attr {
		if a.Key == key && a.Val == val {
			return true
		}
	}
	return false
}

func hasAttrContains(n *html.Node, key, substr string) bool {
	for _, a := range n.Attr {
		if a.Key == key && strings.Contains(a.Val, substr) {
			return true
		}
	}
	return false
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(textContent(c))
	}
	return sb.String()
}

func nextElementSibling(n *html.Node) *html.Node {
	for s := n.NextSibling; s != nil; s = s.NextSibling {
		if s.Type == html.ElementNode {
			return s
		}
	}
	return nil
}

// collapseWhitespace replaces runs of whitespace with a single space and trims.
func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
