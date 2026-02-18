package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

const Version = "0.1"

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

type Config struct {
	YouTubeAPIKey string   `toml:"youtube_api_key"`
	ChannelHandle string   `toml:"channel_handle"`
	YTDLPPath     string   `toml:"yt_dlp_path"`
	WhisperModel  string   `toml:"whisper_model"`
	DataDir       string   `toml:"data_dir"`
	SiteOutputDir string   `toml:"site_output_dir"`
	CheckInterval Duration `toml:"check_interval"`
}

func (c *Config) setDefaults() {
	if c.ChannelHandle == "" {
		c.ChannelHandle = "@WalnutCreekGov"
	}
	if c.YTDLPPath == "" {
		c.YTDLPPath = "yt-dlp"
	}
	if c.WhisperModel == "" {
		c.WhisperModel = "mlx-community/whisper-medium"
	}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if c.SiteOutputDir == "" {
		c.SiteOutputDir = "site"
	}
	if c.CheckInterval.Duration == 0 {
		c.CheckInterval = Duration{30 * time.Minute}
	}
}

func (c *Config) Validate() error {
	if c.YouTubeAPIKey == "" {
		return errors.New("youtube_api_key is required in config file")
	}
	return nil
}

// DatabasePath returns the path to the meetings database file.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "meetings.json")
}

// VideosDir returns the directory where videos are stored.
func (c *Config) VideosDir() string {
	return filepath.Join(c.DataDir, "videos")
}

// AudioDir returns the directory where extracted audio is stored.
func (c *Config) AudioDir() string {
	return filepath.Join(c.DataDir, "audio")
}

// TranscriptsDir returns the directory where transcripts are stored.
func (c *Config) TranscriptsDir() string {
	return filepath.Join(c.DataDir, "transcripts")
}

// AgendasDir returns the directory where agenda PDFs are stored.
func (c *Config) AgendasDir() string {
	return filepath.Join(c.DataDir, "agendas")
}

func checkFile(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

// getConfigPath finds the config file path. Checks:
//   - $XDG_CONFIG_HOME/walnut-creek-meetings
//   - $HOME/cfg/walnut-creek-meetings
//   - $HOME/.walnut-creek-meetings
func getConfigPath() (string, error) {
	const name = "walnut-creek-meetings"
	checkedLocations := make([]string, 0, 3)

	xdgPath, ok := os.LookupEnv("XDG_CONFIG_HOME")
	filePath := filepath.Join(xdgPath, name)
	checkedLocations = append(checkedLocations, filePath)
	if ok && checkFile(filePath) {
		return filePath, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("retrieving home directory: %w", err)
	}

	cfgPath := filepath.Join(homeDir, "cfg", name)
	checkedLocations = append(checkedLocations, cfgPath)
	if checkFile(cfgPath) {
		return cfgPath, nil
	}

	localPath := filepath.Join(homeDir, "."+name)
	checkedLocations = append(checkedLocations, localPath)
	if checkFile(localPath) {
		return localPath, nil
	}

	return "", fmt.Errorf(`config file not found. Checked:
  %s
  %s
  %s

Create a config file with the following contents:

youtube_api_key = "YOUR_YOUTUBE_API_KEY"
channel_handle = "@WalnutCreekGov"
yt_dlp_path = "/path/to/yt-dlp"
whisper_model = "mlx-community/whisper-medium"
data_dir = "data"
site_output_dir = "site"
check_interval = "30m"

Get a YouTube Data API key at https://console.cloud.google.com/apis/credentials
Enable the "YouTube Data API v3" for your project first.
`, checkedLocations[0], checkedLocations[1], checkedLocations[2])
}

// LoadConfig reads and parses the TOML configuration file.
func LoadConfig() (*Config, error) {
	cfgPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(cfgPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cfg Config
	if _, err := toml.NewDecoder(bufio.NewReader(f)).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", cfgPath, err)
	}
	cfg.setDefaults()
	return &cfg, nil
}
