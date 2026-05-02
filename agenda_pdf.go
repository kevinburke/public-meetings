package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// pdfTopLevelRe matches a top-level numbered heading like "  1.  TITLE".
// pdftotext -layout preserves the indentation; the trailing whitespace is
// flexible so titles starting after one or many spaces both match.
var pdfTopLevelRe = regexp.MustCompile(`^\s*(\d+)\.\s+(.*\S)\s*$`)

// pdfSubItemRe matches a sub-item like "    1.1   TITLE". The two segments
// are split on the period so we don't accidentally match top-level headings.
var pdfSubItemRe = regexp.MustCompile(`^\s*(\d+\.\d+)\s+(.*\S)\s*$`)

// ParseAgendaPDF runs pdftotext -layout against a Highbond agenda PDF and
// returns one AgendaItem per numbered heading found.  Sub-items inherit the
// top-level number prefix, indented continuation lines accumulate into the
// most recent item's Description, and per-item document hyperlinks are
// recovered from a separate pdftohtml pass.
func ParseAgendaPDF(ctx context.Context, pdfPath string) ([]AgendaItem, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return nil, fmt.Errorf("pdftotext not installed (poppler-utils): %w", err)
	}
	textOut, err := runPdfTool(ctx, "pdftotext", "-layout", pdfPath, "-")
	if err != nil {
		return nil, err
	}

	items := scanAgendaItemsFromText(textOut)

	// Hyperlink mapping: pdftohtml emits anchor tags whose text we can
	// look up against the description we already accumulated. Failure to
	// run pdftohtml is non-fatal — we'll just return items without
	// document links rather than failing the whole annotation pipeline.
	if links, err := extractPDFHyperlinks(ctx, pdfPath); err == nil {
		attachLinksToItems(items, links)
	}
	return items, nil
}

// pdfHyperlink is one (text, href) pair scraped from pdftohtml output.
type pdfHyperlink struct {
	Text string
	URL  string
}

func extractPDFHyperlinks(ctx context.Context, pdfPath string) ([]pdfHyperlink, error) {
	if _, err := exec.LookPath("pdftohtml"); err != nil {
		return nil, fmt.Errorf("pdftohtml not installed (poppler-utils): %w", err)
	}
	out, err := runPdfTool(ctx, "pdftohtml", "-i", "-stdout", "-hidden", pdfPath)
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(bytes.NewReader(out))
	if err != nil {
		return nil, err
	}
	var links []pdfHyperlink
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := getAttr(n, "href")
			text := collapseWhitespace(textContent(n))
			if href != "" && text != "" {
				links = append(links, pdfHyperlink{Text: text, URL: href})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return links, nil
}

func runPdfTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w (%s)", name, err, stderr.String())
	}
	return out, nil
}

// scanAgendaItemsFromText is the line-walker half of ParseAgendaPDF; split
// out so tests can drive it from inline strings without poppler installed.
func scanAgendaItemsFromText(text []byte) []AgendaItem {
	var items []AgendaItem
	scanner := bufio.NewScanner(bytes.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			continue
		}

		// Sub-items (1.1, 6.2, 11.3) are matched first because the
		// top-level regex would otherwise greedily eat the leading
		// "1" before the dot.
		if m := pdfSubItemRe.FindStringSubmatch(line); m != nil {
			items = append(items, AgendaItem{
				Number: m[1],
				Title:  collapseWhitespace(m[2]),
			})
			continue
		}
		if m := pdfTopLevelRe.FindStringSubmatch(line); m != nil {
			items = append(items, AgendaItem{
				Number: m[1],
				Title:  collapseWhitespace(m[2]),
			})
			continue
		}

		// Continuation: append to the most recent item's description.
		// Skip header text (anything before the first numbered item).
		if len(items) == 0 {
			continue
		}
		last := &items[len(items)-1]
		text := collapseWhitespace(line)
		if text == "" {
			continue
		}
		if last.Description == "" {
			last.Description = text
		} else {
			last.Description += " " + text
		}
	}
	return items
}

// attachLinksToItems heuristically maps each pdftohtml-extracted hyperlink
// to the AgendaItem whose description contains the link text. Imperfect but
// good enough for the templated downstream renderer; per-link false matches
// are tolerable since the agenda PDF is also linked in full.
func attachLinksToItems(items []AgendaItem, links []pdfHyperlink) {
	if len(items) == 0 || len(links) == 0 {
		return
	}
	for _, link := range links {
		// Skip anchor-style links pdftohtml emits for cross-page nav,
		// which always start with a filename like "wcsd-agenda.html#1".
		if strings.HasPrefix(link.URL, "wcsd-agenda.html") {
			continue
		}
		if link.Text == "" {
			continue
		}
		// Walk items in declaration order; first whose description or
		// title contains the link text wins. Tied scoring is fine —
		// the downstream renderer just needs a plausible parent.
		matched := false
		needle := link.Text
		for i := range items {
			if strings.Contains(items[i].Title, needle) || strings.Contains(items[i].Description, needle) {
				items[i].Documents = append(items[i].Documents, AgendaDocument{
					Title: link.Text,
					URL:   link.URL,
				})
				matched = true
				break
			}
		}
		if !matched && len(items) > 0 {
			// Last-resort: attach to the last item we saw.
			last := &items[len(items)-1]
			last.Documents = append(last.Documents, AgendaDocument{
				Title: link.Text,
				URL:   link.URL,
			})
		}
	}
}

// LoadAgendaItems is the source-agnostic loader for the annotator and the
// site renderer. If a PDF agenda is on disk (Highbond instances) we run the
// PDF parser; otherwise we fall through to ParseAgendaHTML (Granicus).
// Returns a non-nil error only when something is structurally broken — a
// missing-file return value is `nil, nil` so the caller can render the page
// without an agenda block.
func LoadAgendaItems(ctx context.Context, m *Meeting) ([]AgendaItem, error) {
	pdfPath := agendaPDFPath(m)
	if _, err := os.Stat(pdfPath); err == nil {
		return ParseAgendaPDF(ctx, pdfPath)
	}
	htmlPath := agendaHTMLPath(m)
	if _, err := os.Stat(htmlPath); err == nil {
		return ParseAgendaHTML(htmlPath)
	}
	return nil, nil
}
