package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// findWhisper locates the mlx_whisper binary. It checks, in order:
//  1. <project-root>/venv/bin/mlx_whisper (project-local install from make setup)
//  2. The system PATH
func findWhisper() (string, error) {
	venvWhisper := filepath.Join(projectRoot(), "venv", "bin", "mlx_whisper")
	if _, err := os.Stat(venvWhisper); err == nil {
		return venvWhisper, nil
	}

	// Fall back to PATH
	path, err := exec.LookPath("mlx_whisper")
	if err != nil {
		return "", fmt.Errorf("mlx_whisper not found: install it with 'make setup' or 'pip install mlx-whisper': %w", err)
	}
	return path, nil
}

// audioDuration uses ffprobe to get the duration of an audio file in seconds.
func audioDuration(ctx context.Context, path string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	return strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
}

// whisperTimestampRe matches lines like "[00:33.880 --> 00:36.720]  some text"
// and also "[01:00:02.540 --> 01:00:07.980]  some text" (HH:MM:SS.mmm after 1 hour).
var whisperTimestampRe = regexp.MustCompile(`^\[(\d{2}:\d{2}(?::\d{2})?\.\d{3}) --> (\d{2}:\d{2}(?::\d{2})?\.\d{3})\]`)

// parseWhisperTS parses a whisper timestamp like "36:20.000" (MM:SS.mmm) or
// "01:00:02.540" (HH:MM:SS.mmm) into seconds.
func parseWhisperTS(s string) float64 {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		// MM:SS.mmm
		min, _ := strconv.ParseFloat(parts[0], 64)
		sec, _ := strconv.ParseFloat(parts[1], 64)
		return min*60 + sec
	case 3:
		// HH:MM:SS.mmm
		hr, _ := strconv.ParseFloat(parts[0], 64)
		min, _ := strconv.ParseFloat(parts[1], 64)
		sec, _ := strconv.ParseFloat(parts[2], 64)
		return hr*3600 + min*60 + sec
	default:
		return 0
	}
}

// progressWriter reads whisper's stdout line by line and logs progress as a
// percentage of the total audio duration.
type progressWriter struct {
	meeting      string
	totalSeconds float64
	segments     int
	lastPct      int // last percentage we logged, to avoid repeating
}

func (pw *progressWriter) processLine(line string) {
	if !whisperTimestampRe.MatchString(line) {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintln(os.Stderr, line)
		}
		return
	}

	pw.segments++
	m := whisperTimestampRe.FindStringSubmatch(line)
	if m == nil || pw.totalSeconds <= 0 {
		return
	}

	endSec := parseWhisperTS(m[2])
	pct := min(int(endSec/pw.totalSeconds*100), 100)

	// Log at every 10% increment
	milestone := (pct / 10) * 10
	if milestone > pw.lastPct && milestone > 0 {
		pw.lastPct = milestone
		slog.Info("transcription progress",
			"meeting", pw.meeting,
			"progress", fmt.Sprintf("%d%%", milestone),
		)
	}
}

// TranscribeAudio runs mlx-whisper on an audio file and produces a VTT
// transcript with timestamps.
func TranscribeAudio(ctx context.Context, cfg *Config, meeting *Meeting) error {
	if meeting.AudioPath == "" {
		return fmt.Errorf("no audio path set for meeting %s", meeting.ID)
	}

	whisperPath, err := findWhisper()
	if err != nil {
		return err
	}

	totalSec, err := audioDuration(ctx, meeting.AudioPath)
	if err != nil {
		slog.Warn("could not determine audio duration, progress will not be shown", "error", err)
	}

	transcriptDir := cfg.TranscriptsDir()
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		return fmt.Errorf("creating transcript directory: %w", err)
	}

	durStr := "unknown"
	if totalSec > 0 {
		durStr = (time.Duration(totalSec) * time.Second).String()
	}
	slog.Info("transcribing audio",
		"meeting", meeting.ID,
		"title", meeting.Title,
		"duration", durStr,
		"model", cfg.WhisperModel,
	)

	args := []string{
		meeting.AudioPath,
		"--model", cfg.WhisperModel,
		"--language", "en",
		"--output-format", "vtt",
		"--output-dir", transcriptDir,
	}
	cmd := exec.CommandContext(ctx, whisperPath, args...)
	// Force Python to use unbuffered stdout/stderr so we see progress
	// lines as they're emitted rather than all at once at exit.
	// https://docs.python.org/3/using/cmdline.html#envvar-PYTHONUNBUFFERED
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting mlx_whisper: %w", err)
	}

	pw := &progressWriter{
		meeting:      meeting.ID,
		totalSeconds: totalSec,
	}

	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			pw.processLine(scanner.Text())
		}
	}()

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		pw.processLine(scanner.Text())
	}
	io.Copy(io.Discard, stdoutPipe)

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("running mlx_whisper: %w", err)
	}

	// whisper outputs <basename>.vtt in the output dir
	base := filepath.Base(meeting.AudioPath)
	ext := filepath.Ext(base)
	vttName := base[:len(base)-len(ext)] + ".vtt"
	vttPath := filepath.Join(transcriptDir, vttName)

	if _, err := os.Stat(vttPath); err != nil {
		return fmt.Errorf("expected transcript at %s but not found: %w", vttPath, err)
	}

	meeting.TranscriptPath = vttPath
	slog.Info("transcription complete",
		"meeting", meeting.ID,
		"segments", pw.segments,
		"path", vttPath,
	)
	return nil
}
