package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// VTTCue represents a single cue (subtitle segment) from a VTT file.
type VTTCue struct {
	Start time.Duration
	End   time.Duration
	Text  string
}

// StartSeconds returns the start time as a float in seconds.
func (c VTTCue) StartSeconds() float64 {
	return c.Start.Seconds()
}

// StartTimestamp returns the start time formatted as HH:MM:SS.
func (c VTTCue) StartTimestamp() string {
	h := int(c.Start.Hours())
	m := int(c.Start.Minutes()) % 60
	s := int(c.Start.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// vttTimestampRe matches both HH:MM:SS.mmm and MM:SS.mmm formats.
var vttTimestampRe = regexp.MustCompile(`(?:(\d{2}):)?(\d{2}):(\d{2})\.(\d{3})`)

func parseVTTTimestamp(s string) (time.Duration, error) {
	matches := vttTimestampRe.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("invalid VTT timestamp: %q", s)
	}
	var h int
	if matches[1] != "" {
		h, _ = strconv.Atoi(matches[1])
	}
	m, _ := strconv.Atoi(matches[2])
	sec, _ := strconv.Atoi(matches[3])
	ms, _ := strconv.Atoi(matches[4])

	return time.Duration(h)*time.Hour +
		time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second +
		time.Duration(ms)*time.Millisecond, nil
}

// ParseVTT reads a WebVTT file and returns the list of cues.
func ParseVTT(path string) ([]VTTCue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cues []VTTCue
	scanner := bufio.NewScanner(f)

	// Skip the WEBVTT header
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Check if this is a timing line (contains " --> ")
		if !strings.Contains(line, " --> ") {
			// Could be a cue identifier, skip it and read next line
			if !scanner.Scan() {
				break
			}
			line = strings.TrimSpace(scanner.Text())
			if !strings.Contains(line, " --> ") {
				continue
			}
		}

		// Parse timing line
		parts := strings.SplitN(line, " --> ", 2)
		if len(parts) != 2 {
			continue
		}

		// Strip any position/alignment metadata after the end timestamp
		endParts := strings.Fields(parts[1])
		start, err := parseVTTTimestamp(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		end, err := parseVTTTimestamp(endParts[0])
		if err != nil {
			continue
		}

		// Read text lines until empty line
		var textLines []string
		for scanner.Scan() {
			textLine := scanner.Text()
			if strings.TrimSpace(textLine) == "" {
				break
			}
			textLines = append(textLines, textLine)
		}

		if len(textLines) > 0 {
			cues = append(cues, VTTCue{
				Start: start,
				End:   end,
				Text:  strings.Join(textLines, " "),
			})
		}
	}

	return cues, scanner.Err()
}
