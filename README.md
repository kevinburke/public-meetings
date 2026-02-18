# walnut-creek-meetings

Monitors the City of Walnut Creek YouTube channel for new meeting videos,
downloads them, transcribes them with [mlx-whisper], and generates a static
website with searchable transcripts and agenda links.

Supports City Council, Planning Commission, and Design Review Commission
meetings.

[mlx-whisper]: https://github.com/ml-explore/mlx-examples/tree/main/whisper

## Requirements

- Go 1.21+
- Python 3 (for mlx-whisper)
- macOS with Apple Silicon (mlx-whisper uses the MLX framework)
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)
- A YouTube Data API v3 key

## Setup

1. Create a config file at one of:
   - `$XDG_CONFIG_HOME/walnut-creek-meetings`
   - `~/cfg/walnut-creek-meetings`
   - `~/.walnut-creek-meetings`

   With contents:

   ```toml
   youtube_api_key = "YOUR_YOUTUBE_API_KEY"
   ```

   Get a YouTube Data API key at https://console.cloud.google.com/apis/credentials
   (enable "YouTube Data API v3" for your project first).

   Optional fields with their defaults:

   ```toml
   channel_handle = "@WalnutCreekGov"
   yt_dlp_path = "yt-dlp"
   whisper_model = "mlx-community/whisper-medium"
   data_dir = "data"
   site_output_dir = "site"
   check_interval = "30m"
   ```

2. Install dependencies:

   ```
   make setup
   ```

   This creates a Python venv with mlx-whisper, downloads Deno (used by
   yt-dlp), and builds the Go binary.

## Usage

### Discover new meetings

```
walnut-creek-meetings check
```

Queries the YouTube API for recent videos and adds any new meetings to the
database.

### Process meetings

```
walnut-creek-meetings process
```

Downloads, transcribes, and fetches agendas for all pending meetings. Each
meeting progresses through: download (yt-dlp) -> transcribe (mlx-whisper) ->
fetch agenda (Granicus RSS) -> generate site.

### Regenerate the site

```
walnut-creek-meetings generate
```

Regenerates the static HTML site from existing data. Output goes to the
configured `site_output_dir` (default: `site/`).

### Annotate transcripts with agenda items

```
walnut-creek-meetings annotate <meeting-id>
```

Uses [Codex](https://github.com/openai/codex) to analyze a meeting's transcript
and agenda, producing a JSON file that maps agenda items to the timestamps where
they are discussed. The meeting must already have a transcript and downloaded
agenda HTML (run `process` first). Results are saved to
`var/artifacts/<meeting-id>-annotations.json` and displayed as a clickable table
of contents on the meeting page.

### Run continuously

```
walnut-creek-meetings watch
```

Runs in a loop, periodically checking for new videos and processing them
automatically. Ctrl-C to stop.

### Preview the site locally

```
make serve
```

Starts a local HTTPS server to preview the generated site.

## Typical workflow

```
walnut-creek-meetings check              # discover new meetings
walnut-creek-meetings process            # download, transcribe, fetch agendas
walnut-creek-meetings annotate <id>      # (optional) annotate with agenda items
walnut-creek-meetings generate           # regenerate the site
```

Or just run `walnut-creek-meetings watch` to do it all continuously.

## Project layout

```
config.go       Configuration loading (TOML)
main.go         CLI entrypoint and subcommands
meeting.go      Meeting types, ID generation, date parsing
youtube.go      YouTube API client
transcribe.go   mlx-whisper transcription
vtt.go          WebVTT transcript parsing
agenda.go       Granicus RSS feed + agenda HTML parsing
annotate.go     Codex-powered transcript annotation
site.go         Static site generation (HTML templates)
cmd/serve/      Local HTTPS preview server
data/           Database and downloaded files (gitignored)
var/            Artifacts and working files (gitignored)
site/           Generated static site output
```
