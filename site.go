package main

import (
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Walnut Creek Meeting Transcripts</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 900px; margin: 0 auto; padding: 20px; line-height: 1.6; }
h1 { border-bottom: 2px solid #333; padding-bottom: 10px; }
h2 { margin-top: 2em; }
.meeting-list { list-style: none; padding: 0; }
.meeting-list li { padding: 8px 0; border-bottom: 1px solid #eee; }
.meeting-list a { text-decoration: none; color: #0066cc; }
.meeting-list a:hover { text-decoration: underline; }
.meeting-date { color: #666; font-size: 0.9em; margin-right: 1em; }
.meeting-body { display: inline-block; padding: 2px 8px; border-radius: 3px; font-size: 0.8em; font-weight: bold; }
.body-city-council { background: #e3f2fd; color: #1565c0; }
.body-planning-commission { background: #e8f5e9; color: #2e7d32; }
.body-design-review-commission { background: #fff3e0; color: #e65100; }
.meeting-summary { color: #555; font-size: 0.9em; margin-top: 4px; }
</style>
</head>
<body>
<h1>Walnut Creek Meeting Transcripts</h1>
<p>Searchable transcripts of Walnut Creek city government meetings.</p>
{{range .Bodies}}
<h2>{{.Name}}</h2>
<ul class="meeting-list">
{{range .Meetings}}
<li>
<span class="meeting-date">{{.DateFormatted}}</span>
<a href="{{.Href}}">{{.Title}}</a>
{{if .HasAgenda}} &middot; <a href="{{.AgendaURL}}">Agenda</a>{{end}}
{{if .MeetingSummary}}<div class="meeting-summary">{{.MeetingSummary}}</div>{{end}}
</li>
{{end}}
</ul>
{{end}}
</body>
</html>
`))

var meetingTemplate = template.Must(template.New("meeting").Funcs(template.FuncMap{
	"bodyClass": func(b MeetingBody) string {
		return "body-" + string(b)
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} - Walnut Creek Meetings</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 1100px; margin: 0 auto; padding: 20px; line-height: 1.6; }
a { color: #0066cc; }
.back-link { margin-bottom: 1em; }
h1 { font-size: 1.5em; }
.video-sticky { position: sticky; top: 0; z-index: 10; background: #fff; padding-bottom: 10px; }
.video-container { position: relative; padding-bottom: 56.25%; height: 0; overflow: hidden; }
.video-container iframe { position: absolute; top: 0; left: 0; width: 100%; height: 100%; }
.agenda-link { margin: 1em 0; padding: 10px 15px; background: #f5f5f5; border-radius: 5px; display: inline-block; }
.transcript { margin-top: 2em; }
.transcript h2 { border-bottom: 1px solid #ddd; padding-bottom: 5px; }
.agenda-content { margin: 1.5em 0; }
.agenda-content h2 { border-bottom: 1px solid #ddd; padding-bottom: 5px; }
.agenda-item { margin-bottom: 1.5em; padding: 12px 16px; background: #fafafa; border: 1px solid #e8e8e8; border-radius: 5px; }
.agenda-item h3 { margin: 0 0 8px; font-size: 1em; }
.agenda-desc { color: #444; font-size: 0.95em; white-space: pre-line; margin: 8px 0; }
.agenda-staff-report { margin: 8px 0; }
.agenda-staff-report a { font-weight: 600; }
.agenda-docs { margin-top: 8px; }
.agenda-docs summary { cursor: pointer; color: #0066cc; font-size: 0.9em; }
.agenda-docs ul { margin: 6px 0 0; padding-left: 1.5em; font-size: 0.9em; }
.agenda-docs li { padding: 2px 0; }
.section-header { margin: 1.5em 0 0.5em; padding: 8px 12px; background: #e3f2fd; border-left: 4px solid #1565c0; border-radius: 0 4px 4px 0; font-weight: 600; font-size: 1.05em; scroll-margin-top: 400px; }
.cue { padding: 4px 0; display: flex; gap: 12px; border-bottom: 1px solid #f0f0f0; }
.cue:hover { background: #f9f9f9; }
.cue-time { flex-shrink: 0; width: 80px; color: #0066cc; cursor: pointer; font-family: monospace; font-size: 0.9em; padding-top: 2px; }
.cue-time:hover { text-decoration: underline; }
.cue-text { flex: 1; }
.toc { margin: 1.5em 0; padding: 15px 20px; background: #f8f9fa; border: 1px solid #e0e0e0; border-radius: 5px; }
.toc h2 { margin-top: 0; font-size: 1.2em; }
.toc-list { padding-left: 1.5em; }
.toc-list li { padding: 4px 0; }
.toc-time { font-family: monospace; color: #0066cc; cursor: pointer; margin-right: 8px; }
.toc-time:hover { text-decoration: underline; }
.toc-title { font-weight: 500; }
.toc-summary { display: block; color: #666; font-size: 0.9em; margin-top: 2px; }
.search-box { margin: 1em 0; }
.search-box input { width: 100%; max-width: 400px; padding: 8px 12px; font-size: 1em; border: 1px solid #ccc; border-radius: 4px; }
.highlight { background: #fff176; }
.hidden { display: none; }
</style>
</head>
<body>
<div class="back-link"><a href="index.html">&larr; All Meetings</a></div>
<h1>{{.Title}}</h1>
<p>{{.DateFormatted}} &middot; {{.BodyName}}</p>

<div class="video-sticky">
<div class="video-container">
<iframe id="player" src="https://www.youtube.com/embed/{{.YouTubeID}}?enablejsapi=1" frameborder="0" allowfullscreen allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"></iframe>
</div>
</div>

{{if .AgendaURL}}
<div class="agenda-link"><a href="{{.AgendaURL}}" target="_blank">View Meeting Agenda</a></div>
{{end}}

{{if .AgendaItems}}
<div class="agenda-content">
<h2>Agenda</h2>
{{range .AgendaItems}}
<div class="agenda-item">
<h3>{{.Number}}. {{.Title}}</h3>
{{if .Description}}<p class="agenda-desc">{{.Description}}</p>{{end}}
{{if .StaffReport}}<div class="agenda-staff-report"><a href="{{.StaffReport.URL}}" target="_blank">Staff Report</a></div>{{end}}
{{if .OtherDocs}}
<details class="agenda-docs">
<summary>Attachments ({{len .OtherDocs}})</summary>
<ul>
{{range .OtherDocs}}<li><a href="{{.URL}}" target="_blank">{{.Title}}</a></li>
{{end}}
</ul>
</details>
{{end}}
</div>
{{end}}
</div>
{{end}}

{{if .Annotations}}
<div class="toc">
<h2>Agenda Items</h2>
<ol class="toc-list">
{{range .Annotations}}
<li>
<span class="toc-time" onclick="jumpTo({{.StartSeconds}}, 'agenda-{{.Number}}')">{{.StartTimestamp}}</span>
<span class="toc-title">{{.Title}}</span>
{{if .Summary}}<span class="toc-summary">{{.Summary}}</span>{{end}}
</li>
{{end}}
</ol>
</div>
{{end}}

{{if .Cues}}
<div class="transcript">
<h2>Transcript</h2>
<div class="search-box">
<input type="text" id="search" placeholder="Search transcript..." oninput="filterTranscript(this.value)">
</div>
<div id="cues">
{{range .Cues}}{{if .SectionID}}
<div class="section-header" id="{{.SectionID}}">{{.SectionTitle}}</div>
{{end}}
<div class="cue" data-start="{{.StartSeconds}}">
<span class="cue-time" onclick="seekTo({{.StartSeconds}})">{{.StartTimestamp}}</span>
<span class="cue-text">{{.Text}}</span>
</div>
{{end}}
</div>
</div>

<script>
var player;
var tag = document.createElement('script');
tag.src = "https://www.youtube.com/iframe_api";
var firstScriptTag = document.getElementsByTagName('script')[0];
firstScriptTag.parentNode.insertBefore(tag, firstScriptTag);

function onYouTubeIframeAPIReady() {
    player = new YT.Player('player', {
        events: { 'onReady': function() {} }
    });
}

function seekTo(seconds) {
    if (player && player.seekTo) {
        player.seekTo(seconds, true);
        player.playVideo();
    }
}

function jumpTo(seconds, sectionId) {
    seekTo(seconds);
    var el = document.getElementById(sectionId);
    if (el) {
        el.scrollIntoView({behavior: 'smooth', block: 'start'});
    }
}

function filterTranscript(query) {
    var cues = document.querySelectorAll('.cue');
    var lower = query.toLowerCase();
    cues.forEach(function(cue) {
        var text = cue.querySelector('.cue-text');
        var original = text.getAttribute('data-original');
        if (!original) {
            original = text.textContent;
            text.setAttribute('data-original', original);
        }
        if (lower === '') {
            cue.classList.remove('hidden');
            text.textContent = original;
            return;
        }
        if (original.toLowerCase().indexOf(lower) === -1) {
            cue.classList.add('hidden');
            text.textContent = original;
        } else {
            cue.classList.remove('hidden');
            var re = new RegExp('(' + query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ')', 'gi');
            text.innerHTML = original.replace(re, '<span class="highlight">$1</span>');
        }
    });
}
</script>
{{end}}
</body>
</html>
`))

// transcriptCorrections maps mis-transcribed words/phrases to their correct
// forms. Keys are case-insensitive patterns; values are the replacement text.
// The replacement preserves the position in the string but not the original case.
var transcriptCorrections = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)\bShadeland'?s\b`), "Shadelands"},
}

// correctTranscript applies spelling corrections to transcript text.
func correctTranscript(s string) string {
	for _, c := range transcriptCorrections {
		s = c.re.ReplaceAllString(s, c.replacement)
	}
	return s
}

// correctTranscriptSimple applies simple case-insensitive word replacements.
// This is a convenience for adding entries that don't need regex.
var simpleCorrections = map[string]string{
	// Add simple word replacements here. These are matched as whole words,
	// case-insensitive.
	// "misspelled": "correct",
}

func init() {
	for wrong, right := range simpleCorrections {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(wrong) + `\b`)
		transcriptCorrections = append(transcriptCorrections, struct {
			re          *regexp.Regexp
			replacement string
		}{re, right})
	}
}

type indexBodyData struct {
	Name     string
	Meetings []indexMeetingData
}

type indexMeetingData struct {
	DateFormatted  string
	Title          string
	Href           string
	HasAgenda      bool
	AgendaURL      string
	MeetingSummary string
}

// templateCue is a transcript cue for rendering, optionally preceded by
// an agenda section header.
type templateCue struct {
	StartSeconds   float64
	StartTimestamp string
	Text           string
	SectionID      string // non-empty if this cue starts a new agenda section
	SectionTitle   string
}

// templateAgendaItem holds parsed agenda content for rendering on the meeting page.
type templateAgendaItem struct {
	Number      string
	Title       string
	Description string           // project description text
	StaffReport *AgendaDocument  // staff report link (shown prominently)
	OtherDocs   []AgendaDocument // remaining attachments (collapsible)
}

type meetingPageData struct {
	Title          string
	DateFormatted  string
	BodyName       string
	YouTubeID      string
	AgendaURL      string
	MeetingSummary string
	AgendaItems    []templateAgendaItem
	Cues           []templateCue
	Annotations    []AgendaAnnotation
}

type indexPageData struct {
	Bodies []indexBodyData
}

// GenerateSite generates the static HTML website from the database.
func GenerateSite(cfg *Config, db *Database) error {
	outDir := cfg.SiteOutputDir
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	db.mu.Lock()
	meetings := make([]*Meeting, len(db.Meetings))
	copy(meetings, db.Meetings)
	db.mu.Unlock()

	// Sort meetings by date descending
	sort.Slice(meetings, func(i, j int) bool {
		return meetings[i].Date.After(meetings[j].Date)
	})

	// Generate individual meeting pages
	for _, m := range meetings {
		if m.Status != StatusTranscribed && m.Status != StatusComplete {
			continue
		}
		if err := generateMeetingPage(outDir, m); err != nil {
			slog.Error("generating meeting page", "meeting", m.ID, "error", err)
			continue
		}
	}

	// Generate index page
	if err := generateIndexPage(outDir, meetings); err != nil {
		return fmt.Errorf("generating index page: %w", err)
	}

	slog.Info("site generated", "output", outDir)
	return nil
}

func generateMeetingPage(outDir string, m *Meeting) error {
	data := meetingPageData{
		Title:         m.Title,
		DateFormatted: m.Date.Format("January 2, 2006"),
		BodyName:      m.Body.DisplayName(),
		YouTubeID:     m.YouTubeID,
		AgendaURL:     m.AgendaURL,
	}

	result, _ := LoadAnnotations(m.ID)
	if result != nil {
		data.Annotations = result.Items
		data.MeetingSummary = result.MeetingSummary
	}

	// Load parsed agenda items if the HTML is available
	agendaHTMLPath := filepath.Join(projectRoot(), "var", "artifacts", m.ID+".html")
	if agendaItems, err := ParseAgendaHTML(agendaHTMLPath); err == nil {
		for _, item := range agendaItems {
			if item.Description == "" && len(item.Documents) == 0 {
				continue // skip items with no content beyond the title
			}
			tai := templateAgendaItem{
				Number:      item.Number,
				Title:       item.Title,
				Description: item.Description,
			}
			for _, doc := range item.Documents {
				lower := strings.ToLower(doc.Title)
				if strings.Contains(lower, "staff report") && tai.StaffReport == nil {
					d := doc
					tai.StaffReport = &d
				} else {
					tai.OtherDocs = append(tai.OtherDocs, doc)
				}
			}
			data.AgendaItems = append(data.AgendaItems, tai)
		}
	}

	if m.TranscriptPath != "" {
		cues, err := ParseVTT(m.TranscriptPath)
		if err != nil {
			slog.Warn("could not parse transcript", "path", m.TranscriptPath, "error", err)
		} else {
			// Build templateCues, injecting section headers from annotations.
			ai := 0 // index into annotations
			for _, cue := range cues {
				tc := templateCue{
					StartSeconds:   cue.StartSeconds(),
					StartTimestamp: cue.StartTimestamp(),
					Text:           correctTranscript(cue.Text),
				}
				// If the next annotation starts at or before this cue, attach it.
				if ai < len(data.Annotations) && data.Annotations[ai].StartSeconds <= cue.StartSeconds() {
					tc.SectionID = "agenda-" + data.Annotations[ai].Number
					tc.SectionTitle = data.Annotations[ai].Number + ". " + data.Annotations[ai].Title
					ai++
				}
				data.Cues = append(data.Cues, tc)
			}
		}
	}

	outPath := filepath.Join(outDir, m.ID+".html")
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return meetingTemplate.Execute(f, data)
}

func generateIndexPage(outDir string, meetings []*Meeting) error {
	bodyOrder := []MeetingBody{CityCouncil, PlanningCommission, DesignReviewCommission}
	grouped := make(map[MeetingBody][]indexMeetingData)

	for _, m := range meetings {
		if m.Status != StatusTranscribed && m.Status != StatusComplete {
			continue
		}
		entry := indexMeetingData{
			DateFormatted: m.Date.Format("Jan 2, 2006"),
			Title:         m.Title,
			Href:          m.ID + ".html",
			HasAgenda:     m.AgendaURL != "",
			AgendaURL:     m.AgendaURL,
		}
		if result, err := LoadAnnotations(m.ID); err == nil {
			entry.MeetingSummary = result.MeetingSummary
		}
		grouped[m.Body] = append(grouped[m.Body], entry)
	}

	var bodies []indexBodyData
	for _, b := range bodyOrder {
		if ms, ok := grouped[b]; ok && len(ms) > 0 {
			bodies = append(bodies, indexBodyData{
				Name:     b.DisplayName(),
				Meetings: ms,
			})
		}
	}

	data := indexPageData{Bodies: bodies}

	outPath := filepath.Join(outDir, "index.html")
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Also write a simple stylesheet
	if err := indexTemplate.Execute(f, data); err != nil {
		return err
	}

	return nil
}
