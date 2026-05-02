package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AnnotationResult is the top-level output from the codex annotation step.
type AnnotationResult struct {
	MeetingSummary string             `json:"meeting_summary"` // 1-2 sentence overview
	Items          []AgendaAnnotation `json:"items"`
}

// AgendaAnnotation maps an agenda item to the transcript timestamp where its
// discussion begins.
type AgendaAnnotation struct {
	Number         string  `json:"number"`            // e.g. "4a"
	Title          string  `json:"title"`             // agenda item title
	StartTimestamp string  `json:"start_timestamp"`   // e.g. "01:23:45"
	StartSeconds   float64 `json:"start_seconds"`     // e.g. 5025
	Summary        string  `json:"summary,omitempty"` // optional brief summary
}

// AnnotateMeeting uses codex to match agenda items to transcript timestamps.
// It writes the result to var/artifacts/<meeting-id>-annotations.json.
func AnnotateMeeting(ctx context.Context, meeting *Meeting) error {
	if meeting.TranscriptPath == "" {
		return fmt.Errorf("no transcript for meeting %s", meeting.ID)
	}

	// Pick the right agenda parser by artifact: PDF (Highbond) wins
	// over HTML (Granicus) when both are present, since the PDF carries
	// more structural cues (numbered sub-items, hyperlinks).
	agendaItems, err := LoadAgendaItems(ctx, meeting)
	if err != nil {
		return fmt.Errorf("parsing agenda: %w", err)
	}
	if len(agendaItems) == 0 {
		return fmt.Errorf("no agenda items found for meeting %s; run 'process' first", meeting.ID)
	}

	// Parse transcript
	cues, err := ParseVTT(meeting.TranscriptPath)
	if err != nil {
		return fmt.Errorf("parsing transcript: %w", err)
	}

	// Prepare working directory with input files for codex
	workDir := annotateWorkDir(meeting)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}

	// Write agenda items as a simple text file
	var agendaBuf strings.Builder
	for _, item := range agendaItems {
		fmt.Fprintf(&agendaBuf, "%s. %s\n", item.Number, item.Title)
	}
	agendaFile := filepath.Join(workDir, "agenda.txt")
	if err := os.WriteFile(agendaFile, []byte(agendaBuf.String()), 0o644); err != nil {
		return err
	}

	// Write transcript as timestamped text
	var transBuf strings.Builder
	for _, cue := range cues {
		fmt.Fprintf(&transBuf, "[%s] %s\n", cue.StartTimestamp(), cue.Text)
	}
	transcriptFile := filepath.Join(workDir, "transcript.txt")
	if err := os.WriteFile(transcriptFile, []byte(transBuf.String()), 0o644); err != nil {
		return err
	}

	outputFile := filepath.Join(workDir, "annotations.json")

	slog.Info("running codex to annotate meeting",
		"meeting", meeting.ID,
		"agenda_items", len(agendaItems),
		"transcript_cues", len(cues),
	)

	prompt := fmt.Sprintf(`Read the agenda items in %s and the meeting transcript in %s.

For each agenda item, find the timestamp in the transcript where discussion of that item begins. Write a JSON file to %s with this structure:

{
  "meeting_summary": "A 1-2 sentence summary of the key topics and decisions from the entire meeting.",
  "items": [
    {
      "number": "1",
      "title": "Short title (max 80 chars)",
      "start_timestamp": "HH:MM:SS",
      "start_seconds": 0,
      "summary": "One sentence about what was discussed."
    }
  ]
}

Rules:
- "meeting_summary" should be a concise overview of the whole meeting suitable for display on an index page.
- Only include agenda items that are actually discussed in the transcript.
- Skip procedural items (roll call, pledge of allegiance) unless they have substantive discussion.
- The JSON must be valid and parseable.
- If an agenda item is not discussed (e.g. it was on the consent calendar and passed without discussion), omit it.
- Order items by start_seconds ascending.
`, agendaFile, transcriptFile, outputFile)

	// Shell out to codex
	cmd := exec.CommandContext(ctx, "codex", "exec",
		"--full-auto",
		prompt,
	)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codex exec failed: %w", err)
	}

	// Verify the output file was created and is valid JSON
	data, err := os.ReadFile(outputFile)
	if err != nil {
		return fmt.Errorf("codex did not produce output file %s: %w", outputFile, err)
	}

	var result AnnotationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("codex output is not valid JSON: %w", err)
	}

	// Copy to the standard artifacts location
	destPath := annotationJSONPath(meeting)
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		return err
	}

	slog.Info("annotation complete",
		"meeting", meeting.ID,
		"annotations", len(result.Items),
		"summary", result.MeetingSummary,
		"path", destPath,
	)

	return nil
}

// LoadAnnotations reads the annotations JSON for a meeting if it exists.
func LoadAnnotations(meeting *Meeting) (*AnnotationResult, error) {
	path := annotationJSONPath(meeting)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result AnnotationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing annotations: %w", err)
	}
	return &result, nil
}
