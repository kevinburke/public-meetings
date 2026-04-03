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
	YouTubeAPIKey string           `toml:"youtube_api_key"`
	ChannelHandle string           `toml:"channel_handle"`
	YTDLPPath     string           `toml:"yt_dlp_path"`
	WhisperModel  string           `toml:"whisper_model"`
	DataDir       string           `toml:"data_dir"`
	SiteOutputDir string           `toml:"site_output_dir"`
	CheckInterval Duration         `toml:"check_interval"`
	Instances     []InstanceConfig `toml:"instances"`
}

type InstanceConfig struct {
	Slug          string `toml:"slug"`
	Name          string `toml:"name"`
	Description   string `toml:"description"`
	ChannelHandle string `toml:"channel_handle"`
	AgendaRSSURL  string `toml:"agenda_rss_url"`
	TimeZone      string `toml:"time_zone"`
}

func defaultInstance() InstanceConfig {
	return InstanceConfig{
		Slug:          "walnut-creek",
		Name:          "Walnut Creek",
		Description:   "Searchable transcripts of Walnut Creek city government meetings.",
		ChannelHandle: "@WalnutCreekGov",
		AgendaRSSURL:  "https://walnutcreek.granicus.com/ViewPublisherRSS.php?view_id=12&mode=agendas",
		TimeZone:      "America/Los_Angeles",
	}
}

func (ic *InstanceConfig) setDefaults() {
	def := defaultInstance()
	if ic.Slug == "" {
		ic.Slug = def.Slug
	}
	if ic.Name == "" {
		ic.Name = def.Name
	}
	if ic.Description == "" {
		ic.Description = fmt.Sprintf("Searchable transcripts of %s meetings.", ic.Name)
	}
	if ic.ChannelHandle == "" {
		ic.ChannelHandle = def.ChannelHandle
	}
	if ic.AgendaRSSURL == "" {
		ic.AgendaRSSURL = def.AgendaRSSURL
	}
	if ic.TimeZone == "" {
		ic.TimeZone = def.TimeZone
	}
}

func (c *Config) setDefaults() {
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
	if len(c.Instances) == 0 {
		inst := defaultInstance()
		if c.ChannelHandle != "" {
			inst.ChannelHandle = c.ChannelHandle
		}
		c.Instances = []InstanceConfig{inst}
	}
	for i := range c.Instances {
		c.Instances[i].setDefaults()
	}
}

func (c *Config) Validate() error {
	if c.YouTubeAPIKey == "" {
		return errors.New("youtube_api_key is required in config file")
	}
	seen := make(map[string]struct{}, len(c.Instances))
	for _, inst := range c.Instances {
		if inst.Slug == "" {
			return errors.New("instances.slug is required")
		}
		if inst.ChannelHandle == "" {
			return fmt.Errorf("instances[%s].channel_handle is required", inst.Slug)
		}
		if _, err := time.LoadLocation(inst.TimeZone); err != nil {
			return fmt.Errorf("instances[%s].time_zone: %w", inst.Slug, err)
		}
		if _, ok := seen[inst.Slug]; ok {
			return fmt.Errorf("duplicate instance slug %q", inst.Slug)
		}
		seen[inst.Slug] = struct{}{}
	}
	return nil
}

// DatabasePath returns the path to the meetings database file.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "meetings.json")
}

func (c *Config) instanceDataDir(slug string) string {
	return filepath.Join(c.DataDir, slug)
}

// VideosDir returns the directory where videos are stored.
func (c *Config) VideosDir(slug string) string {
	return filepath.Join(c.instanceDataDir(slug), "videos")
}

// AudioDir returns the directory where extracted audio is stored.
func (c *Config) AudioDir(slug string) string {
	return filepath.Join(c.instanceDataDir(slug), "audio")
}

// TranscriptsDir returns the directory where transcripts are stored.
func (c *Config) TranscriptsDir(slug string) string {
	return filepath.Join(c.instanceDataDir(slug), "transcripts")
}

// AgendasDir returns the directory where agenda PDFs are stored.
func (c *Config) AgendasDir(slug string) string {
	return filepath.Join(c.instanceDataDir(slug), "agendas")
}

func (c *Config) SiteInstanceDir(slug string) string {
	return filepath.Join(c.SiteOutputDir, slug)
}

func (c *Config) DefaultInstanceSlug() string {
	if len(c.Instances) == 0 {
		return defaultInstance().Slug
	}
	return c.Instances[0].Slug
}

func (c *Config) InstanceForSlug(slug string) (*InstanceConfig, bool) {
	for i := range c.Instances {
		if c.Instances[i].Slug == slug {
			return &c.Instances[i], true
		}
	}
	return nil, false
}

func checkFile(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

// getConfigPath finds the config file path. Checks:
//   - $XDG_CONFIG_HOME/public-meetings
//   - $HOME/cfg/public-meetings
//   - $HOME/.public-meetings
func getConfigPath() (string, error) {
	const name = "public-meetings"
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
yt_dlp_path = "/path/to/yt-dlp"
whisper_model = "mlx-community/whisper-medium"
data_dir = "data"
site_output_dir = "site"
check_interval = "30m"

[[instances]]
slug = "walnut-creek"
name = "Walnut Creek"
channel_handle = "@WalnutCreekGov"
agenda_rss_url = "https://walnutcreek.granicus.com/ViewPublisherRSS.php?view_id=12&mode=agendas"
time_zone = "America/Los_Angeles"

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
