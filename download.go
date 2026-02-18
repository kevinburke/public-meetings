package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Errors from yt-dlp that indicate the video is not available and retrying
// won't help.
var noRetryErrors = []string{
	"live event will begin in",
	"Premieres in",
	"This video is not available",
	"Video unavailable",
	"Private video",
	"This video has been removed",
	"Sign in to confirm your age",
}

// projectRoot returns the root of the git repository, or the directory of the
// running executable as a fallback.
func projectRoot() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	// Fallback: directory containing the executable
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

// findJSRuntime locates a JavaScript runtime for yt-dlp. Checks in order:
//  1. <project-root>/bin/deno (project-local install from make setup)
//  2. deno on PATH
//  3. node on PATH
func findJSRuntime() (name string, path string) {
	// Check project-local deno first
	localDeno := filepath.Join(projectRoot(), "bin", "deno")
	if _, err := os.Stat(localDeno); err == nil {
		return "deno", localDeno
	}

	// Check PATH for deno
	if p, err := exec.LookPath("deno"); err == nil {
		return "deno", p
	}

	// Fall back to node
	if p, err := exec.LookPath("node"); err == nil {
		return "node", p
	}

	return "", ""
}

// DownloadVideo downloads a YouTube video using yt-dlp.
// It retries on failure to handle YouTube's post-processing delays, but
// gives up immediately on errors that won't resolve with time (e.g. the video
// is a future live event).
func DownloadVideo(ctx context.Context, cfg *Config, meeting *Meeting) error {
	videoDir := cfg.VideosDir()
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		return fmt.Errorf("creating video directory: %w", err)
	}

	outputTemplate := filepath.Join(videoDir, meeting.ID+".%(ext)s")
	ytURL := "https://www.youtube.com/watch?v=" + meeting.YouTubeID

	jsName, jsPath := findJSRuntime()
	if jsName == "" {
		return fmt.Errorf("no JavaScript runtime found (need deno or node); run 'make setup' to install deno")
	}
	jsArg := jsName + ":" + jsPath

	slog.Info("downloading video",
		"meeting", meeting.ID,
		"title", meeting.Title,
		"body", meeting.Body.DisplayName(),
		"date", meeting.Date.Format("2006-01-02"),
		"url", ytURL,
		"js_runtime", jsArg,
	)

	maxRetries := 5
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			slog.Info("download attempt", "attempt", attempt, "meeting", meeting.ID)
		}

		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, cfg.YTDLPPath,
			"--output", outputTemplate,
			"--format", "bestaudio[ext=m4a]/bestaudio/best",
			"--no-overwrites",
			"--write-info-json",
			"--restrict-filenames",
			"--js-runtimes", jsArg,
			ytURL,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		if err == nil {
			break
		}

		stderrStr := stderr.String()
		// Always show yt-dlp's output so the user can see what happened
		os.Stderr.WriteString(stderrStr)

		if isNoRetryError(stderrStr) {
			return fmt.Errorf("video not available: %w", err)
		}

		if attempt == maxRetries {
			return fmt.Errorf("downloading video after %d attempts: %w", maxRetries, err)
		}

		delay := time.Duration(attempt*5) * time.Minute
		slog.Warn("download failed, retrying", "attempt", attempt, "delay", delay, "error", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	// Find the downloaded file
	matches, err := filepath.Glob(filepath.Join(videoDir, meeting.ID+".*"))
	if err != nil {
		return fmt.Errorf("finding downloaded file: %w", err)
	}
	for _, match := range matches {
		ext := filepath.Ext(match)
		if ext == ".json" {
			continue
		}
		meeting.AudioPath = match
		slog.Info("downloaded audio", "path", match)
		return nil
	}

	return fmt.Errorf("no audio file found after download for %s", meeting.ID)
}

func isNoRetryError(stderr string) bool {
	for _, msg := range noRetryErrors {
		if strings.Contains(stderr, msg) {
			return true
		}
	}
	return false
}
