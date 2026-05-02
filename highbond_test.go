package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHighbondListings(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "wcsd-MeetingTypeList.html"))
	if err != nil {
		t.Fatal(err)
	}
	listings, err := parseHighbondListings(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(listings) == 0 {
		t.Fatal("no listings parsed")
	}
	t.Logf("parsed %d listings", len(listings))

	var foundApr13 bool
	var foundClosedSession bool
	for _, l := range listings {
		t.Logf("  type=%q title=%q path=%s", l.TypeName, l.Title, l.MeetingPath)
		if strings.Contains(l.Title, "Apr 13 2026") && strings.Contains(l.MeetingPath, "Id=288") {
			foundApr13 = true
			if l.TypeName != "Walnut Creek School District Regular Governing Board Meeting" {
				t.Errorf("Apr 13 listing type = %q, want Regular Governing Board Meeting", l.TypeName)
			}
		}
		if strings.Contains(strings.ToLower(l.TypeName), "closed session") {
			foundClosedSession = true
		}
	}
	if !foundApr13 {
		t.Error("did not find the Apr 13 2026 regular meeting (Id=288)")
	}
	if !foundClosedSession {
		t.Error("did not find any CLOSED SESSION meeting type")
	}
}

func TestParseHighbondTitleDate(t *testing.T) {
	tests := []struct {
		title string
		want  string // YYYY-MM-DD or "" for parse failure
	}{
		{"Walnut Creek School District Regular Governing Board Meeting - Apr 13 2026", "2026-04-13"},
		{"WCSD Governing Board - SPECIAL MEETING - Mar 11 2026", "2026-03-11"},
		{"CANCELLED - WCSD Governing Board Meeting - CLOSED SESSION - Sep 08 2025", "2025-09-08"},
		{"Some title with no date in it", ""},
		{"Strategic Planning Meeting - March 23, 2026", "2026-03-23"},
	}
	for _, tc := range tests {
		got, ok := parseHighbondTitleDate(tc.title)
		if tc.want == "" {
			if ok {
				t.Errorf("parseHighbondTitleDate(%q) = %v, want failure", tc.title, got.Format("2006-01-02"))
			}
			continue
		}
		if !ok {
			t.Errorf("parseHighbondTitleDate(%q) failed", tc.title)
			continue
		}
		if got.Format("2006-01-02") != tc.want {
			t.Errorf("parseHighbondTitleDate(%q) = %s, want %s", tc.title, got.Format("2006-01-02"), tc.want)
		}
	}
}

func TestClassifyHighbondMeeting(t *testing.T) {
	tests := []struct {
		typeName, title string
		body            MeetingBody
		session         string
	}{
		{"Walnut Creek School District Regular Governing Board Meeting", "Regular Meeting - Apr 13 2026", SchoolBoard, ""},
		{"Walnut Creek School District Governing Board Meeting - CLOSED SESSION", "WCSD Governing Board - CLOSED SESSION - Sep 22 2025", SchoolBoard, "closed-session"},
		{"Governing Board - Special Meeting", "Special Meeting of the WCSD Governing Board - Jul 14 2025", SchoolBoard, "special-meeting"},
		{"Strategic Planning Meeting", "WCSD Strategic Planning Meeting - Mar 23 2026", SchoolBoard, "strategic-planning"},
		{"Walnut Creek School District NOTICE OF PROPOSED TEXTBOOK ADOPTION", "Notice - 2025", "", ""},
		{"NOTICE OF PUBLIC HEARING", "Public Hearing - Some Date", "", ""},
		{"Governing Board - Notice of Board Quorum", "Notice of Board Quorum - 2026", "", ""},
		{"Public Notice - Developer Fee Resolution", "Developer Fee Resolution Notice", "", ""},
	}
	for _, tc := range tests {
		gotBody, gotSession := classifyHighbondMeeting(tc.typeName, tc.title)
		if gotBody != tc.body || gotSession != tc.session {
			t.Errorf("classifyHighbondMeeting(%q, %q) = (%q, %q), want (%q, %q)", tc.typeName, tc.title, gotBody, gotSession, tc.body, tc.session)
		}
	}
}

func TestExtractAgendaPDFURL(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "wcsd-MeetingInformation-288.html"))
	if err != nil {
		t.Fatal(err)
	}
	got := extractAgendaPDFURL(body)
	if got == "" {
		t.Fatal("no agenda PDF URL extracted")
	}
	if !strings.Contains(got, "printPdf=true") {
		t.Errorf("agenda URL missing printPdf=true: %s", got)
	}
	if !strings.Contains(got, "/document/12997/") {
		t.Errorf("agenda URL has wrong document id: %s", got)
	}
	if !strings.HasPrefix(got, "https://") {
		t.Errorf("agenda URL is not https: %s", got)
	}
}

func TestVimeoLinkRegex(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"watch live: https://vimeo.com/event/5633543 thanks", "https://vimeo.com/event/5633543"},
		{"https://www.vimeo.com/event/123/abc456?foo=bar", "https://www.vimeo.com/event/123/abc456"},
		{"https://vimeo.com/987654321 trailing", "https://vimeo.com/987654321"},
		{"no link here", ""},
		{"http://vimeo.com/event/12345", "http://vimeo.com/event/12345"},
	}
	for _, tc := range tests {
		got := vimeoLinkRe.FindString(tc.in)
		if got != tc.want {
			t.Errorf("vimeoLinkRe.FindString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTypeMatches(t *testing.T) {
	tests := []struct {
		typeName string
		filters  []string
		want     bool
	}{
		{"Regular Governing Board Meeting", nil, true}, // empty filter accepts all
		{"Regular Governing Board Meeting", []string{"regular"}, true},
		{"BoardDocs Imported Meetings 2017-2025", []string{"regular", "closed", "special", "strategic"}, false},
		{"Walnut Creek School District Governing Board Meeting - CLOSED SESSION", []string{"closed"}, true},
		{"Strategic Planning Meeting", []string{"strategic"}, true},
		{"NOTICE OF PUBLIC HEARING", []string{"regular", "closed"}, false},
	}
	for _, tc := range tests {
		got := typeMatches(tc.typeName, lowerSlice(tc.filters))
		if got != tc.want {
			t.Errorf("typeMatches(%q, %v) = %v, want %v", tc.typeName, tc.filters, got, tc.want)
		}
	}
}
