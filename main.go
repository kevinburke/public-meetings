// The walnut-creek-meetings tool monitors the City of Walnut Creek YouTube
// channel for new meeting videos, downloads them, transcribes them with
// Whisper, and generates a static website with searchable transcripts.
//
// Usage:
//
//	walnut-creek-meetings <command> [arguments]
//
// Commands:
//
//	watch     Run continuously, checking for new videos and processing them
//	check     Check for new videos once and exit
//	process   Process all pending meetings (download, transcribe, fetch agenda)
//	generate  Regenerate the static site from existing data
//	annotate  Use codex to match agenda items to transcript timestamps
//	version   Print the version
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

const help = `The walnut-creek-meetings tool monitors the City of Walnut Creek YouTube
channel for new meeting videos, downloads and transcribes them, and generates
a static website with searchable transcripts.

Usage:

	walnut-creek-meetings <command> [arguments]

Commands:

	watch     Run continuously, checking for new videos and processing them
	check     Check for new videos once and exit
	process   Process all pending meetings (download, transcribe, fetch agenda)
	generate  Regenerate the static site from existing data
	annotate  Use codex to match agenda items to transcript timestamps
	version   Print the version

Use "walnut-creek-meetings <command> --help" for more information about a command.
`

func usage() {
	fmt.Fprint(os.Stderr, help)
	flag.PrintDefaults()
}

func init() {
	flag.Usage = usage
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	if *debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(os.Stdout, "walnut-creek-meetings version %s\n", Version)
		os.Exit(0)
	case "watch":
		runWatch(ctx, args[1:])
	case "check":
		runCheck(ctx, args[1:])
	case "process":
		runProcess(ctx, args[1:])
	case "generate":
		runGenerate(args[1:])
	case "annotate":
		runAnnotate(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "walnut-creek-meetings: unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func loadConfigAndDB() (*Config, *Database) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}
	db, err := LoadDatabase(cfg.DatabasePath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading database: %v\n", err)
		os.Exit(1)
	}
	return cfg, db
}

// runCheck checks for new videos once and exits.
func runCheck(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	lookbackDays := fs.Int("lookback-days", 180, "How many days back to search for videos")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: walnut-creek-meetings check [flags]

Query the YouTube API for recent videos from the configured channel and add
any new city council, planning commission, or design review commission
meetings to the database.

Flags:
`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	cfg, db := loadConfigAndDB()
	yt := NewYouTubeClient(cfg.YouTubeAPIKey)

	channelID, err := yt.ResolveChannelID(ctx, cfg.ChannelHandle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving channel: %v\n", err)
		os.Exit(1)
	}
	slog.Info("resolved channel", "handle", cfg.ChannelHandle, "id", channelID)

	since := time.Now().AddDate(0, 0, -*lookbackDays)
	newMeetings, err := CheckForNewMeetings(ctx, yt, channelID, db, since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking for new meetings: %v\n", err)
		os.Exit(1)
	}

	if err := db.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving database: %v\n", err)
		os.Exit(1)
	}

	if len(newMeetings) == 0 {
		slog.Info("no new meetings found")
	} else {
		slog.Info("found new meetings", "count", len(newMeetings))
		for _, m := range newMeetings {
			fmt.Printf("  %s: %s\n", m.ID, m.Title)
		}
	}
}

// runProcess processes all meetings that need work.
func runProcess(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("process", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: walnut-creek-meetings process

Download, transcribe, and fetch agendas for all pending meetings in the
database. Each meeting progresses through: download (yt-dlp) -> transcribe
(whisper) -> fetch agenda (Granicus) -> generate site.

Run 'walnut-creek-meetings check' first to discover new meetings.
`)
	}
	fs.Parse(args)

	cfg, db := loadConfigAndDB()
	processMeetings(ctx, cfg, db)
}

func processMeetings(ctx context.Context, cfg *Config, db *Database) {
	if len(db.Meetings) == 0 {
		slog.Info("no meetings in database; run 'walnut-creek-meetings check' first to discover videos")
		return
	}

	// Process meetings in order: download, transcribe, fetch agenda
	for _, m := range db.Meetings {
		if ctx.Err() != nil {
			return
		}

		switch m.Status {
		case StatusNew:
			m.Status = StatusDownloading
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
			}

			if err := DownloadVideo(ctx, cfg, m); err != nil {
				slog.Error("downloading video", "meeting", m.ID, "error", err)
				m.Status = StatusNew // reset so we retry
				db.Save()
				continue
			}

			m.Status = StatusDownloaded
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
			}
			fallthrough

		case StatusDownloaded:
			m.Status = StatusTranscribing
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
			}

			if err := TranscribeAudio(ctx, cfg, m); err != nil {
				slog.Error("transcribing audio", "meeting", m.ID, "error", err)
				m.Status = StatusDownloaded // reset so we retry
				db.Save()
				continue
			}

			m.Status = StatusTranscribed
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
			}
			fallthrough

		case StatusTranscribed:
			if err := DownloadAgenda(ctx, cfg, m); err != nil {
				slog.Error("fetching agenda", "meeting", m.ID, "error", err)
				// Non-fatal: continue without agenda
			}

			m.Status = StatusComplete
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
			}

			// Regenerate site after each completed meeting
			if err := GenerateSite(cfg, db); err != nil {
				slog.Error("generating site", "error", err)
			}
		}
	}
}

// runGenerate regenerates the static site.
func runGenerate(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: walnut-creek-meetings generate

Regenerate the static HTML website from existing data in the database.
Outputs to the configured site_output_dir (default: site/).
`)
	}
	fs.Parse(args)

	cfg, db := loadConfigAndDB()
	if err := GenerateSite(cfg, db); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating site: %v\n", err)
		os.Exit(1)
	}
}

// runAnnotate uses codex to match agenda items to transcript timestamps.
func runAnnotate(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("annotate", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: walnut-creek-meetings annotate <meeting-id>

Use codex to analyze a meeting's transcript and agenda, and produce a JSON
file mapping agenda items to the timestamps where they are discussed.

The meeting must have a transcript and a downloaded agenda HTML in
var/artifacts/. Run 'process' first to ensure these exist.

Example:
    walnut-creek-meetings annotate 2026-02-03-city-council
`)
	}
	fs.Parse(args)

	_, db := loadConfigAndDB()

	if fs.NArg() < 1 {
		// List meetings eligible for annotation
		var eligible []*Meeting
		for _, m := range db.Meetings {
			if m.TranscriptPath == "" {
				continue
			}
			agendaPath := filepath.Join(projectRoot(), "var", "artifacts", m.ID+".html")
			if _, err := os.Stat(agendaPath); err != nil {
				continue
			}
			eligible = append(eligible, m)
		}
		if len(eligible) == 0 {
			fmt.Fprintf(os.Stderr, "No meetings are ready for annotation.\nRun 'process' first to download transcripts and agendas.\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Usage: walnut-creek-meetings annotate <meeting-id>\n\nMeetings available for annotation:\n")
		for _, m := range eligible {
			annotationPath := filepath.Join(projectRoot(), "var", "artifacts", m.ID+"-annotations.json")
			status := ""
			if _, err := os.Stat(annotationPath); err == nil {
				status = " (already annotated)"
			}
			fmt.Fprintf(os.Stderr, "  %s%s\n", m.ID, status)
		}
		os.Exit(2)
	}
	meetingID := fs.Arg(0)

	// Find the meeting in the database
	var meeting *Meeting
	for _, m := range db.Meetings {
		if m.ID == meetingID {
			meeting = m
			break
		}
	}
	if meeting == nil {
		fmt.Fprintf(os.Stderr, "meeting %q not found in database\n", meetingID)
		os.Exit(1)
	}

	if err := AnnotateMeeting(ctx, meeting); err != nil {
		fmt.Fprintf(os.Stderr, "Error annotating meeting: %v\n", err)
		os.Exit(1)
	}
}

// runWatch runs continuously, checking for new videos and processing them.
func runWatch(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	lookbackDays := fs.Int("lookback-days", 14, "How many days back to search on first check")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: walnut-creek-meetings watch [flags]

Run continuously, periodically checking for new meeting videos on YouTube.
When new meetings are found, automatically download, transcribe, fetch
agendas, and regenerate the site. Ctrl-C to stop.

Flags:
`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	cfg, db := loadConfigAndDB()
	yt := NewYouTubeClient(cfg.YouTubeAPIKey)

	channelID, err := yt.ResolveChannelID(ctx, cfg.ChannelHandle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving channel: %v\n", err)
		os.Exit(1)
	}
	slog.Info("resolved channel", "handle", cfg.ChannelHandle, "id", channelID)
	slog.Info("starting watcher", "interval", cfg.CheckInterval.Duration)

	// First check uses the lookback window
	since := time.Now().AddDate(0, 0, -*lookbackDays)

	for {
		newMeetings, err := CheckForNewMeetings(ctx, yt, channelID, db, since)
		if err != nil {
			slog.Error("checking for new meetings", "error", err)
		} else {
			if len(newMeetings) > 0 {
				slog.Info("found new meetings", "count", len(newMeetings))
				if err := db.Save(); err != nil {
					slog.Error("saving database", "error", err)
				}
			}
		}

		// Process any meetings that need work
		processMeetings(ctx, cfg, db)

		// Subsequent checks only look at recent uploads
		since = time.Now().Add(-cfg.CheckInterval.Duration * 2)

		slog.Info("sleeping until next check", "duration", cfg.CheckInterval.Duration)
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return
		case <-time.After(cfg.CheckInterval.Duration):
		}
	}
}
