# public-meetings

Monitors one or more public meeting channels, downloads meeting videos,
transcribes them with [mlx-whisper], and generates a static website with
searchable transcripts and agenda links.

The site is organized by instance slug. For example, Walnut Creek pages render
under `/walnut-creek/`.

[mlx-whisper]: https://github.com/ml-explore/mlx-examples/tree/main/whisper

## Requirements

- Go 1.21+
- Python 3 (for mlx-whisper)
- macOS with Apple Silicon (mlx-whisper uses the MLX framework)
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)
- A YouTube Data API v3 key

## Setup

1. Create a config file at one of:
   - `$XDG_CONFIG_HOME/public-meetings`
   - `~/cfg/public-meetings`
   - `~/.public-meetings`

   With contents:

   ```toml
   youtube_api_key = "YOUR_YOUTUBE_API_KEY"

   [[instances]]
   slug = "walnut-creek"
   name = "Walnut Creek"
   description = "Searchable transcripts of Walnut Creek city government meetings."
   channel_handle = "@WalnutCreekGov"
   agenda_rss_url = "https://walnutcreek.granicus.com/ViewPublisherRSS.php?view_id=12&mode=agendas"
   time_zone = "America/Los_Angeles"
   ```

   Get a YouTube Data API key at https://console.cloud.google.com/apis/credentials
   (enable "YouTube Data API v3" for your project first).

   Global optional fields with their defaults:

   ```toml
   yt_dlp_path = "yt-dlp"
   whisper_model = "mlx-community/whisper-medium"
   data_dir = "data"
   site_output_dir = "site"
   check_interval = "30m"
   ```

   Legacy single-instance configs that only set top-level `channel_handle`
   still work; they are treated as the default `walnut-creek` instance.

2. Install dependencies:

   ```
   make setup
   ```

   This creates a Python venv, installs the pinned `mlx-whisper` dependency
   from `requirements.txt`, downloads Deno (used by yt-dlp), and builds the Go
   binary.

## Usage

### Discover new meetings

```
public-meetings check
```

Queries the YouTube API for each configured instance and adds any new meetings
to the database.

### Process meetings

```
public-meetings process
```

Downloads, transcribes, and fetches agendas for all pending meetings. Each
meeting progresses through: download (yt-dlp) -> transcribe (mlx-whisper) ->
fetch agenda (Granicus RSS) -> generate site.

### Regenerate the site

```
public-meetings generate
```

Regenerates the static HTML site from existing data. Output goes to the
configured `site_output_dir` (default: `site/`), with one subdirectory per
instance slug plus a root landing page.

### Annotate transcripts with agenda items

```
public-meetings annotate <meeting-id>
```

Uses [Codex](https://github.com/openai/codex) to analyze a meeting's transcript
and agenda, producing a JSON file that maps agenda items to the timestamps where
they are discussed. The meeting must already have a transcript and downloaded
agenda HTML (run `process` first). Results are saved under
`var/artifacts/<instance-slug>/` and displayed as a clickable table of contents
on the meeting page.

### Run continuously

```
public-meetings watch
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
public-meetings check              # discover new meetings
public-meetings process            # download, transcribe, fetch agendas
public-meetings annotate <id>      # (optional) annotate with agenda items
public-meetings generate           # regenerate the site
```

Or just run `public-meetings watch` to do it all continuously.

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
site/           Generated static site output (`/<slug>/...`)
```
