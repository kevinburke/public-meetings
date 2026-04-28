// The public-meetings tool monitors one or more configured public
// meeting channels, downloads the videos, transcribes them with Whisper, and
// generates a static website with searchable transcripts.
//
// Usage:
//
//	public-meetings <command> [arguments]
//
// Commands:
//
//	watch     Run continuously, checking for new videos and processing them
//	check     Check for new videos once and exit
//	process   Process all pending meetings (download, transcribe, fetch agenda, annotate)
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
	"time"
)

const help = `The public-meetings tool monitors one or more configured
public meeting channels, downloads and transcribes their videos, and generates
a static website with searchable transcripts.

Usage:

	public-meetings <command> [arguments]

Commands:

	watch     Run continuously, checking for new videos and processing them
	check     Check for new videos once and exit
	process   Process all pending meetings (download, transcribe, fetch agenda, annotate)
	generate  Regenerate the static site from existing data
	annotate  Use codex to match agenda items to transcript timestamps
	version   Print the version

Use "public-meetings <command> --help" for more information about a command.
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
		fmt.Fprintf(os.Stdout, "public-meetings version %s\n", Version)
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
		fmt.Fprintf(os.Stderr, "public-meetings: unknown command %q\n\n", args[0])
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
	normalizeMeetingInstances(cfg, db)
	return cfg, db
}

func normalizeMeetingInstances(cfg *Config, db *Database) {
	defaultSlug := cfg.DefaultInstanceSlug()
	for _, m := range db.Meetings {
		if m.InstanceSlug == "" {
			m.InstanceSlug = defaultSlug
		}
	}
}

// runCheck checks for new videos once and exits.
func runCheck(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	lookbackDays := fs.Int("lookback-days", 180, "How many days back to search for videos")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: public-meetings check [flags]

Query the YouTube API for recent videos from each configured instance and add
any newly discovered meetings to the database.

Flags:
`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	cfg, db := loadConfigAndDB()
	yt := NewYouTubeClient(cfg.YouTubeAPIKey)

	since := time.Now().AddDate(0, 0, -*lookbackDays)
	var newMeetings []*Meeting
	for _, inst := range cfg.Instances {
		channelID, err := yt.ResolveChannelID(ctx, inst.ChannelHandle)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving channel for %s: %v\n", inst.Slug, err)
			os.Exit(1)
		}
		slog.Info("resolved channel", "instance", inst.Slug, "handle", inst.ChannelHandle, "id", channelID)

		found, err := CheckForNewMeetings(ctx, yt, &inst, channelID, db, since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking for new meetings for %s: %v\n", inst.Slug, err)
			os.Exit(1)
		}
		newMeetings = append(newMeetings, found...)
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
			fmt.Printf("  %s: %s\n", m.QualifiedID(), m.Title)
		}
	}
}

// runProcess processes all meetings that need work.
func runProcess(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("process", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: public-meetings process

Download, transcribe, fetch agendas, and annotate all pending meetings in
the database. Each meeting progresses through: download (yt-dlp) -> transcribe
(whisper) -> fetch agenda (Granicus) -> annotate (codex) -> generate site.
Meetings that already have annotations are skipped.

Run 'public-meetings check' first to discover meetings.
`)
	}
	fs.Parse(args)

	cfg, db := loadConfigAndDB()
	processMeetings(ctx, cfg, db)
}

func processMeetings(ctx context.Context, cfg *Config, db *Database) {
	if len(db.Meetings) == 0 {
		slog.Info("no meetings in database; run 'public-meetings check' first to discover videos")
		return
	}

	// Resume meetings that were killed mid-pipeline. The error paths in
	// the switch below reset Status on failure, but a SIGKILL (systemd
	// TimeoutStartSec, OOM, VM reboot) skips them, leaving the meeting
	// wedged in a transient state that the switch has no case for —
	// silently dropping it from the site forever.
	var resumed bool
	for _, m := range db.Meetings {
		switch m.Status {
		case StatusDownloading:
			slog.Warn("resuming meeting stuck in downloading", "meeting", m.ID)
			m.Status = StatusNew
			resumed = true
		case StatusTranscribing:
			slog.Warn("resuming meeting stuck in transcribing", "meeting", m.ID)
			m.Status = StatusDownloaded
			resumed = true
		}
	}
	if resumed {
		if err := db.Save(); err != nil {
			slog.Error("saving database after resume", "error", err)
			return
		}
	}

	// Process meetings in order: download, transcribe, fetch agenda, annotate
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
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
			}

			if _, err := annotateMeetingIfNeeded(ctx, m); err != nil {
				slog.Error("annotating meeting", "meeting", m.ID, "error", err)
				// Non-fatal: continue without annotations
			}
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
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

		case StatusComplete:
			annotated, err := annotateMeetingIfNeeded(ctx, m)
			if err != nil {
				slog.Error("annotating meeting", "meeting", m.ID, "error", err)
				continue
			}
			if !annotated {
				continue
			}
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
				return
			}
			// Regenerate site since we produced a new annotation
			if err := GenerateSite(cfg, db); err != nil {
				slog.Error("generating site", "error", err)
			}
		}
	}
}

// annotateMeetingIfNeeded runs annotation for a meeting if the annotation file
// doesn't already exist and the meeting has the required transcript and agenda.
// Returns (false, nil) without doing anything if annotations already exist.
func annotateMeetingIfNeeded(ctx context.Context, m *Meeting) (bool, error) {
	annotationPath := annotationJSONPath(m)
	if _, err := os.Stat(annotationPath); err == nil {
		slog.Info("skipping annotation, already exists", "meeting", m.ID)
		return false, nil
	}
	if err := AnnotateMeeting(ctx, m); err != nil {
		return false, err
	}
	return true, nil
}

// runGenerate regenerates the static site.
func runGenerate(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: public-meetings generate

Regenerate the static HTML website from existing data in the database.
Outputs to the configured site_output_dir (default: site/), with one
subdirectory per configured instance slug.
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
		fmt.Fprintf(os.Stderr, `Usage: public-meetings annotate <meeting-id>

Use codex to analyze a meeting's transcript and agenda, and produce a JSON
file mapping agenda items to the timestamps where they are discussed.

The meeting must have a transcript and a downloaded agenda HTML in
var/artifacts/. Run 'process' first to ensure these exist.

Example:
    public-meetings annotate walnut-creek/2026-02-03-city-council
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
			agendaPath := agendaHTMLPath(m)
			if _, err := os.Stat(agendaPath); err != nil {
				continue
			}
			eligible = append(eligible, m)
		}
		if len(eligible) == 0 {
			fmt.Fprintf(os.Stderr, "No meetings are ready for annotation.\nRun 'process' first to download transcripts and agendas.\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Usage: public-meetings annotate <meeting-id>\n\nMeetings available for annotation:\n")
		for _, m := range eligible {
			annotationPath := annotationJSONPath(m)
			status := ""
			if _, err := os.Stat(annotationPath); err == nil {
				status = " (already annotated)"
			}
			fmt.Fprintf(os.Stderr, "  %s%s\n", m.QualifiedID(), status)
		}
		os.Exit(2)
	}
	meetingID := fs.Arg(0)

	// Find the meeting in the database
	var matches []*Meeting
	for _, m := range db.Meetings {
		if m.ID == meetingID || m.QualifiedID() == meetingID {
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "meeting %q not found in database\n", meetingID)
		os.Exit(1)
	}
	if len(matches) > 1 {
		fmt.Fprintf(os.Stderr, "meeting %q is ambiguous; use one of:\n", meetingID)
		for _, m := range matches {
			fmt.Fprintf(os.Stderr, "  %s\n", m.QualifiedID())
		}
		os.Exit(1)
	}
	meeting := matches[0]

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
		fmt.Fprintf(os.Stderr, `Usage: public-meetings watch [flags]

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

	slog.Info("starting watcher", "interval", cfg.CheckInterval.Duration)

	// First check uses the lookback window
	since := time.Now().AddDate(0, 0, -*lookbackDays)

	for {
		var totalNew int
		for _, inst := range cfg.Instances {
			channelID, err := yt.ResolveChannelID(ctx, inst.ChannelHandle)
			if err != nil {
				slog.Error("resolving channel", "instance", inst.Slug, "error", err)
				continue
			}
			slog.Debug("resolved channel", "instance", inst.Slug, "handle", inst.ChannelHandle, "id", channelID)

			newMeetings, err := CheckForNewMeetings(ctx, yt, &inst, channelID, db, since)
			if err != nil {
				slog.Error("checking for new meetings", "instance", inst.Slug, "error", err)
				continue
			}
			totalNew += len(newMeetings)
		}
		if totalNew > 0 {
			slog.Info("found new meetings", "count", totalNew)
			if err := db.Save(); err != nil {
				slog.Error("saving database", "error", err)
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
