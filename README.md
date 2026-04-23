# public-meetings

Monitors one or more public meeting channels, downloads meeting videos,
transcribes them with Whisper, and generates a static website with searchable
transcripts and agenda links.

The site is organized by instance slug. For example, Walnut Creek pages render
under `/walnut-creek/`.

## Requirements

- Go 1.21+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)
- ffmpeg / ffprobe
- A YouTube Data API v3 key
- A Whisper engine — either [mlx-whisper] (macOS, Apple Silicon) or
  [whisper.cpp] (Linux/CPU); see [Transcription engines](#transcription-engines)

[mlx-whisper]: https://github.com/ml-explore/mlx-examples/tree/main/whisper
[whisper.cpp]: https://github.com/ggml-org/whisper.cpp

## Transcription engines

Two backends are supported; pick via the `transcription_engine` config field.

| Engine        | `transcription_engine` | Platform        | `whisper_model` is…                     |
| ------------- | ---------------------- | --------------- | --------------------------------------- |
| mlx-whisper   | `"mlx"` (default)      | macOS / Apple Silicon | a Hugging Face model id, e.g. `mlx-community/whisper-medium` |
| whisper.cpp   | `"whisper-cpp"`        | Linux / CPU     | a filesystem path to a GGML `.bin` model |

The whisper.cpp backend re-encodes the downloaded audio to 16 kHz mono WAV via
ffmpeg before invoking `whisper-cli`; the mlx backend passes the downloaded
audio straight to `mlx_whisper`. Both engines produce the same WebVTT
transcript format so downstream code (agenda annotation, site generation) is
engine-agnostic.

After a successful transcription the source audio file is deleted — it can
always be re-downloaded from YouTube if a re-transcription is ever needed.

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
   yt_dlp_path          = "yt-dlp"
   transcription_engine = "mlx"                          # or "whisper-cpp"
   whisper_model        = "mlx-community/whisper-medium" # HF id (mlx) or GGML path (whisper-cpp)
   whisper_binary       = ""                             # optional override; otherwise PATH/venv lookup
   data_dir             = "data"
   site_output_dir      = "site"
   check_interval       = "30m"
   ```

   When `transcription_engine = "whisper-cpp"`, `whisper_model` is required
   and must point at a GGML `.bin` model file (there is no sensible default;
   model choice is user-driven).

   Legacy single-instance configs that only set top-level `channel_handle`
   still work; they are treated as the default `walnut-creek` instance.

2. Install dependencies. For the mlx (macOS) engine:

   ```
   make setup
   ```

   This creates a Python venv, installs the pinned `mlx-whisper` dependency
   from `requirements.txt`, downloads Deno (used by yt-dlp), and builds the Go
   binary.

   For the whisper.cpp (Linux) engine, install `whisper-cli` yourself — build
   it from https://github.com/ggml-org/whisper.cpp or use a packaged binary —
   and download a GGML model (for example `ggml-medium.en.bin`). Point
   `whisper_model` at the `.bin` path.

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
