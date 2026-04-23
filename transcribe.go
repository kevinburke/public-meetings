package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// findMLXWhisper locates the mlx_whisper binary. It checks, in order:
//  1. cfg.WhisperBinary (explicit override)
//  2. <project-root>/venv/bin/mlx_whisper (project-local install from make setup)
//  3. The system PATH
func findMLXWhisper(cfg *Config) (string, error) {
	if cfg.WhisperBinary != "" {
		return cfg.WhisperBinary, nil
	}
	venvWhisper := filepath.Join(projectRoot(), "venv", "bin", "mlx_whisper")
	if _, err := os.Stat(venvWhisper); err == nil {
		return venvWhisper, nil
	}
	path, err := exec.LookPath("mlx_whisper")
	if err != nil {
		return "", fmt.Errorf("mlx_whisper not found: install it with 'make setup' or 'pip install mlx-whisper': %w", err)
	}
	return path, nil
}

// findWhisperCpp locates the whisper.cpp CLI. It checks, in order:
//  1. cfg.WhisperBinary (explicit override)
//  2. "whisper-cli" on PATH (current upstream binary name)
//  3. "main" on PATH (legacy binary name; still in use on some distros)
func findWhisperCpp(cfg *Config) (string, error) {
	if cfg.WhisperBinary != "" {
		return cfg.WhisperBinary, nil
	}
	for _, name := range []string{"whisper-cli", "main"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("whisper.cpp binary not found: set whisper_binary in config or install whisper-cli on PATH")
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

// whisperTimestampRe matches lines that begin with a segment timestamp range,
// emitted by both mlx-whisper and whisper.cpp: "[MM:SS.mmm --> MM:SS.mmm]"
// (mlx, short meetings) or "[HH:MM:SS.mmm --> HH:MM:SS.mmm]" (whisper.cpp, and
// mlx once a meeting passes the one-hour mark).
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

// progressWriter consumes transcription output line by line and logs progress
// as a percentage of the total audio duration. It works for either engine
// because both emit "[ts --> ts]" segment headers.
type progressWriter struct {
	meeting      string
	totalSeconds float64
	segments     int
	lastPct      int // last percentage logged, to avoid repeating
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

	// Log at every 10% increment.
	milestone := (pct / 10) * 10
	if milestone > pw.lastPct && milestone > 0 {
		pw.lastPct = milestone
		slog.Info("transcription progress",
			"meeting", pw.meeting,
			"progress", fmt.Sprintf("%d%%", milestone),
		)
	}
}

// runTranscriber starts cmd and streams stdout and stderr through pw. Output
// lines that don't match a timestamp are echoed to our stderr so engine
// warnings remain visible.
func runTranscriber(cmd *exec.Cmd, pw *progressWriter) error {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("creating stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", filepath.Base(cmd.Path), err)
	}

	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			pw.processLine(scanner.Text())
		}
		close(done)
	}()

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		pw.processLine(scanner.Text())
	}
	<-done

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("running %s: %w", filepath.Base(cmd.Path), err)
	}
	return nil
}

// TranscribeAudio runs the configured transcription engine on the meeting's
// audio file and produces a VTT transcript with timestamps. On success the
// source audio file is removed; it can be re-downloaded from YouTube if
// needed.
func TranscribeAudio(ctx context.Context, cfg *Config, meeting *Meeting) error {
	if meeting.AudioPath == "" {
		return fmt.Errorf("no audio path set for meeting %s", meeting.ID)
	}

	transcriptDir := cfg.TranscriptsDir(meeting.InstanceSlug)
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		return fmt.Errorf("creating transcript directory: %w", err)
	}

	totalSec, err := audioDuration(ctx, meeting.AudioPath)
	if err != nil {
		slog.Warn("could not determine audio duration, progress will not be shown", "error", err)
	}
	durStr := "unknown"
	if totalSec > 0 {
		durStr = (time.Duration(totalSec) * time.Second).String()
	}
	slog.Info("transcribing audio",
		"meeting", meeting.ID,
		"title", meeting.Title,
		"duration", durStr,
		"engine", cfg.TranscriptionEngine,
		"model", cfg.WhisperModel,
	)

	pw := &progressWriter{meeting: meeting.ID, totalSeconds: totalSec}

	var vttPath string
	switch cfg.TranscriptionEngine {
	case EngineMLX:
		vttPath, err = transcribeMLX(ctx, cfg, meeting, transcriptDir, pw)
	case EngineWhisperCpp:
		vttPath, err = transcribeWhisperCpp(ctx, cfg, meeting, transcriptDir, pw)
	default:
		return fmt.Errorf("unknown transcription_engine %q", cfg.TranscriptionEngine)
	}
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(vttPath); statErr != nil {
		return fmt.Errorf("expected transcript at %s but not found: %w", vttPath, statErr)
	}

	meeting.TranscriptPath = vttPath
	slog.Info("transcription complete",
		"meeting", meeting.ID,
		"segments", pw.segments,
		"path", vttPath,
	)

	// Audio is no longer needed and can be re-downloaded from YouTube if a
	// re-transcription is ever required. Failing to remove it isn't fatal.
	audioPath := meeting.AudioPath
	if err := os.Remove(audioPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not remove audio file after transcription",
			"path", audioPath,
			"error", err,
		)
	}
	meeting.AudioPath = ""
	return nil
}

// transcribeMLX runs Apple's mlx-whisper and returns the output VTT path.
func transcribeMLX(ctx context.Context, cfg *Config, meeting *Meeting, transcriptDir string, pw *progressWriter) (string, error) {
	whisperPath, err := findMLXWhisper(cfg)
	if err != nil {
		return "", err
	}
	args := []string{
		meeting.AudioPath,
		"--model", cfg.WhisperModel,
		"--language", "en",
		"--output-format", "vtt",
		"--output-dir", transcriptDir,
		// Disable conditioning on previous text to prevent hallucination
		// loops where Whisper gets stuck repeating "MUSIC" or similar for
		// the entire file. The tradeoff is slightly less consistent text
		// across windows, but it prevents complete transcription failures.
		"--condition-on-previous-text", "False",
	}
	cmd := exec.CommandContext(ctx, whisperPath, args...)
	// Force Python to use unbuffered stdout/stderr so we see progress lines
	// as they're emitted rather than all at once at exit.
	// https://docs.python.org/3/using/cmdline.html#envvar-PYTHONUNBUFFERED
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	if err := runTranscriber(cmd, pw); err != nil {
		return "", err
	}
	// mlx_whisper writes <basename>.vtt into --output-dir.
	base := filepath.Base(meeting.AudioPath)
	ext := filepath.Ext(base)
	return filepath.Join(transcriptDir, base[:len(base)-len(ext)]+".vtt"), nil
}

// transcribeWhisperCpp runs whisper.cpp's whisper-cli on the meeting audio.
// whisper.cpp requires 16 kHz mono 16-bit PCM WAV input, so we re-encode via
// ffmpeg into a tempdir first.
func transcribeWhisperCpp(ctx context.Context, cfg *Config, meeting *Meeting, transcriptDir string, pw *progressWriter) (string, error) {
	whisperPath, err := findWhisperCpp(cfg)
	if err != nil {
		return "", err
	}

	tmp, err := os.MkdirTemp("", "whisper-cpp-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	wavPath := filepath.Join(tmp, "audio.wav")
	ffmpeg := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin",           // never read stdin; avoids blocking if ffmpeg thinks it's interactive
		"-hide_banner",       // suppress the build/version banner ffmpeg prints on startup
		"-loglevel", "error", // only print errors (ffmpeg otherwise emits a progress line per second)
		"-y",                    // overwrite wavPath if it exists (can't happen in a fresh tmpdir, but belt-and-suspenders)
		"-i", meeting.AudioPath, // input: the downloaded m4a (or whatever yt-dlp produced)
		// whisper.cpp requires 16 kHz mono 16-bit PCM WAV input; anything
		// else silently produces garbage output or refuses to load.
		"-ac", "1", // downmix to 1 audio channel (mono)
		"-ar", "16000", // resample to 16 kHz
		"-f", "wav", // force WAV container (don't infer from the output extension)
		wavPath, // output path
	)
	ffmpeg.Stdout = os.Stderr
	ffmpeg.Stderr = os.Stderr
	if err := ffmpeg.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg re-encode to 16 kHz mono WAV: %w", err)
	}

	base := filepath.Base(meeting.AudioPath)
	prefix := strings.TrimSuffix(base, filepath.Ext(base))
	outPrefix := filepath.Join(transcriptDir, prefix)

	args := []string{
		"-m", cfg.WhisperModel, // path to a GGML .bin model file (e.g. ggml-medium.en.bin)
		"-f", wavPath, // input WAV (must be 16 kHz mono PCM, prepared by ffmpeg above)
		"-l", "en", // language hint; skip auto-detection for a few seconds of startup speedup
		"-ovtt",          // also emit a WebVTT file alongside the default text output
		"-of", outPrefix, // output filename prefix; whisper-cli appends ".vtt" for -ovtt
		"-t", strconv.Itoa(runtime.NumCPU()), // CPU threads; whisper.cpp scales near-linearly up to physical cores
	}
	cmd := exec.CommandContext(ctx, whisperPath, args...)
	if err := runTranscriber(cmd, pw); err != nil {
		return "", err
	}
	return outPrefix + ".vtt", nil
}
