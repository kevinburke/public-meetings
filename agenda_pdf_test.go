package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanAgendaItemsFromText(t *testing.T) {
	// Synthetic input mirroring pdftotext -layout output for a WCSD agenda.
	const layout = `Walnut Creek School District Regular Governing Board Meeting - Apr 13
2026 Agenda
Monday, April 13, 2026 at 6:00 PM


1.     5:00 PM MEETING CALL TO ORDER

       1.1       Live Stream Meeting Access
                 Please follow this link to access the live steam.

2.     PUBLIC COMMENT ON CLOSED SESSION TOPICS

       2.1       Public Comment on Closed Session Topics
                 This is an opportunity for members of the public to address.

6.   CONSENT CALENDAR

     6.1    Williams Uniform Complaint Quarterly Report
            Quarterly Uniform Complaint_Q3_2025-2026_WCSD.pdf

     6.2    Governing Board Minutes for 9th March 2026
            WCSD Regular Governing Board Meeting - Mar 09 2026 - Minutes - Html
`
	items := scanAgendaItemsFromText([]byte(layout))
	if len(items) == 0 {
		t.Fatal("no items parsed")
	}

	want := map[string]string{
		"1":   "5:00 PM MEETING CALL TO ORDER",
		"1.1": "Live Stream Meeting Access",
		"2":   "PUBLIC COMMENT ON CLOSED SESSION TOPICS",
		"2.1": "Public Comment on Closed Session Topics",
		"6":   "CONSENT CALENDAR",
		"6.1": "Williams Uniform Complaint Quarterly Report",
		"6.2": "Governing Board Minutes for 9th March 2026",
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.Number] = it.Title
	}
	for num, title := range want {
		if g := got[num]; g != title {
			t.Errorf("item %s: got title %q, want %q", num, g, title)
		}
	}

	// 6.1 should have its filename as a description (continuation line).
	for _, it := range items {
		if it.Number != "6.1" {
			continue
		}
		if !strings.Contains(it.Description, "Quarterly Uniform Complaint") {
			t.Errorf("item 6.1 description missing filename: %q", it.Description)
		}
	}
}

func TestParseAgendaPDF(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	pdfPath := filepath.Join("testdata", "wcsd-agenda-288.pdf")
	items, err := ParseAgendaPDF(context.Background(), pdfPath)
	if err != nil {
		t.Fatalf("ParseAgendaPDF: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no items parsed")
	}
	t.Logf("parsed %d items from %s", len(items), pdfPath)

	// Spot-check a few well-known items.
	got := map[string]AgendaItem{}
	for _, it := range items {
		got[it.Number] = it
	}
	for _, num := range []string{"1", "1.1", "6.1"} {
		if _, ok := got[num]; !ok {
			t.Errorf("missing item %s; got numbers: %v", num, mapKeys(got))
		}
	}
	// 1.1 (Live Stream) should have grabbed the Vimeo link as a doc.
	if it := got["1.1"]; it.Number == "1.1" {
		var hasVimeo bool
		for _, doc := range it.Documents {
			if strings.Contains(doc.URL, "vimeo.com") {
				hasVimeo = true
			}
		}
		if !hasVimeo {
			t.Logf("1.1 docs: %+v", it.Documents)
			t.Error("item 1.1 (Live Stream) does not have a vimeo document")
		}
	}
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
