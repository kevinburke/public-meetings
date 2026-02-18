package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const youtubeAPIBase = "https://www.googleapis.com/youtube/v3"

// pacificTZ is the America/Los_Angeles timezone for Walnut Creek, CA.
var pacificTZ = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic("could not load America/Los_Angeles timezone: " + err.Error())
	}
	return loc
}()

// YouTubeClient interacts with the YouTube Data API v3.
type YouTubeClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewYouTubeClient(apiKey string) *YouTubeClient {
	return &YouTubeClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// youtubeChannelResponse is the response from the channels.list endpoint.
type youtubeChannelResponse struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
}

// youtubeSearchResponse is the response from the search.list endpoint.
type youtubeSearchResponse struct {
	Items         []youtubeSearchItem `json:"items"`
	NextPageToken string              `json:"nextPageToken"`
}

type youtubeSearchItem struct {
	ID struct {
		VideoID string `json:"videoId"`
	} `json:"id"`
	Snippet struct {
		Title                string    `json:"title"`
		Description          string    `json:"description"`
		PublishedAt          time.Time `json:"publishedAt"`
		LiveBroadcastContent string    `json:"liveBroadcastContent"` // "none", "upcoming", "live"
	} `json:"snippet"`
}

// youtubeVideoResponse is the response from the videos.list endpoint.
type youtubeVideoResponse struct {
	Items []youtubeVideoItem `json:"items"`
}

type youtubeVideoItem struct {
	ID      string `json:"id"`
	Snippet struct {
		Title       string    `json:"title"`
		Description string    `json:"description"`
		PublishedAt time.Time `json:"publishedAt"`
	} `json:"snippet"`
	LiveStreamingDetails struct {
		ActualStartTime *time.Time `json:"actualStartTime"`
	} `json:"liveStreamingDetails"`
}

// youtubeErrorResponse represents a YouTube API error.
type youtubeErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ResolveChannelID looks up a channel ID from a handle like "@WalnutCreekGov".
func (c *YouTubeClient) ResolveChannelID(ctx context.Context, handle string) (string, error) {
	params := url.Values{
		"part":      {"id"},
		"forHandle": {handle},
		"key":       {c.apiKey},
	}
	reqURL := youtubeAPIBase + "/channels?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", err
	}

	var resp youtubeChannelResponse
	if err := c.doJSON(req, &resp); err != nil {
		return "", fmt.Errorf("resolving channel handle %q: %w", handle, err)
	}
	if len(resp.Items) == 0 {
		return "", fmt.Errorf("no channel found for handle %q", handle)
	}
	return resp.Items[0].ID, nil
}

// SearchRecentVideos searches for recent videos on a channel.
// publishedAfter limits results to videos published after this time.
// If zero, returns the most recent videos.
func (c *YouTubeClient) SearchRecentVideos(ctx context.Context, channelID string, publishedAfter time.Time) ([]youtubeSearchItem, error) {
	params := url.Values{
		"part":       {"snippet"},
		"channelId":  {channelID},
		"type":       {"video"},
		"order":      {"date"},
		"maxResults": {"50"},
		"key":        {c.apiKey},
	}
	if !publishedAfter.IsZero() {
		params.Set("publishedAfter", publishedAfter.Format(time.RFC3339))
	}

	reqURL := youtubeAPIBase + "/search?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	var resp youtubeSearchResponse
	if err := c.doJSON(req, &resp); err != nil {
		return nil, fmt.Errorf("searching channel videos: %w", err)
	}
	return resp.Items, nil
}

// GetVideoDetails fetches detailed info for a video, including
// liveStreamingDetails which has the actual start time of a livestream.
func (c *YouTubeClient) GetVideoDetails(ctx context.Context, videoID string) (*youtubeVideoItem, error) {
	params := url.Values{
		"part": {"snippet,liveStreamingDetails"},
		"id":   {videoID},
		"key":  {c.apiKey},
	}

	reqURL := youtubeAPIBase + "/videos?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	var resp youtubeVideoResponse
	if err := c.doJSON(req, &resp); err != nil {
		return nil, fmt.Errorf("fetching video details: %w", err)
	}
	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("video %s not found", videoID)
	}
	return &resp.Items[0], nil
}

// doJSON performs an HTTP request and decodes the JSON response.
func (c *YouTubeClient) doJSON(req *http.Request, target interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp youtubeErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			return fmt.Errorf("youtube API error %d: %s", errResp.Error.Code, errResp.Error.Message)
		}
		return fmt.Errorf("youtube API HTTP %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// CheckForNewMeetings queries YouTube for recent videos and returns any that
// match meeting types and aren't already in the database.
func CheckForNewMeetings(ctx context.Context, yt *YouTubeClient, channelID string, db *Database, since time.Time) ([]*Meeting, error) {
	items, err := yt.SearchRecentVideos(ctx, channelID, since)
	if err != nil {
		return nil, err
	}

	var newMeetings []*Meeting
	for _, item := range items {
		videoID := item.ID.VideoID
		if videoID == "" {
			continue
		}
		// Skip scheduled/live events that haven't completed yet
		if item.Snippet.LiveBroadcastContent == "upcoming" || item.Snippet.LiveBroadcastContent == "live" {
			slog.Debug("skipping upcoming/live video", "title", item.Snippet.Title, "id", videoID, "status", item.Snippet.LiveBroadcastContent)
			continue
		}
		if db.FindByYouTubeID(videoID) != nil {
			continue
		}

		body := classifyVideo(item.Snippet.Title)
		if body == "" {
			slog.Debug("skipping non-meeting video", "title", item.Snippet.Title, "id", videoID)
			continue
		}

		// Determine the actual meeting date. Priority:
		// 1. liveStreamingDetails.actualStartTime in Pacific time (most reliable)
		// 2. Date parsed from the video title
		// 3. publishedAt (fallback)
		meetingDate := parseMeetingDate(item.Snippet.Title, item.Snippet.PublishedAt)
		details, err := yt.GetVideoDetails(ctx, videoID)
		if err != nil {
			slog.Warn("could not fetch video details", "id", videoID, "error", err)
		} else if details.LiveStreamingDetails.ActualStartTime != nil {
			// Convert UTC to Pacific time before extracting the date,
			// since a 5pm Pacific meeting is already the next day in UTC.
			pacific := details.LiveStreamingDetails.ActualStartTime.In(pacificTZ)
			meetingDate = time.Date(pacific.Year(), pacific.Month(), pacific.Day(), 0, 0, 0, 0, time.UTC)
		}

		session := sessionFromTitle(item.Snippet.Title)
		m := &Meeting{
			ID:          MeetingID(meetingDate, body, session),
			Date:        meetingDate,
			Body:        body,
			Session:     session,
			Title:       item.Snippet.Title,
			YouTubeID:   videoID,
			Status:      StatusNew,
			PublishedAt: item.Snippet.PublishedAt,
		}

		if db.Add(m) {
			newMeetings = append(newMeetings, m)
			slog.Info("found new meeting", "title", m.Title, "body", m.Body, "date", m.Date.Format("2006-01-02"), "youtube_id", m.YouTubeID)
		}
	}
	return newMeetings, nil
}
