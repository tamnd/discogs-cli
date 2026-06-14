// Package discogs is the library behind the discogs command line:
// the HTTP client, request shaping, and the typed data models for the
// Discogs music database API.
//
// The Client here is the spine every command shares. It sets a real
// User-Agent, paces requests so a busy session stays polite, and retries
// transient failures (429 and 5xx). Build endpoint calls and JSON decoding
// on top of it.
package discogs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// DefaultUserAgent identifies the client to Discogs. A real, honest
// User-Agent is both polite and the thing most likely to keep you unblocked.
const DefaultUserAgent = "discogs-cli/0.1.0 (github.com/tamnd/discogs-cli)"

// Host is the API host this client talks to.
const Host = "api.discogs.com"

// BaseURL is the root every request is built from.
const BaseURL = "https://" + Host

// Client talks to the Discogs API over HTTP.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// Rate is the minimum gap between requests. Zero means no pacing.
	Rate    time.Duration
	Retries int

	last time.Time
}

// NewClient returns a Client with sensible defaults: a 30s timeout, a 200ms
// minimum gap between requests, and three retries on transient errors.
func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: DefaultUserAgent,
		Rate:      200 * time.Millisecond,
		Retries:   3,
	}
}

// Get fetches url and returns the response body. It paces and retries
// according to the client's settings.
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", url, lastErr)
}

func (c *Client) do(ctx context.Context, url string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has passed since the previous request.
func (c *Client) pace() {
	if c.Rate <= 0 {
		return
	}
	if wait := c.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// --- Output types ---

// SearchResult is one item returned by the database search endpoint.
type SearchResult struct {
	ID    int    `kit:"id" json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	URI   string `json:"uri"`
	Thumb string `json:"thumb"`
}

// Artist is a Discogs artist record.
type Artist struct {
	ID      int      `kit:"id" json:"id"`
	Name    string   `json:"name"`
	Profile string   `json:"profile"`
	URLs    []string `json:"urls"`
	Members []string `json:"members"`
}

// Release is a Discogs release record.
type Release struct {
	ID        int      `kit:"id" json:"id"`
	Title     string   `json:"title"`
	Year      int      `json:"year"`
	Artists   []string `json:"artists"`
	Genres    []string `json:"genres"`
	Styles    []string `json:"styles"`
	Tracklist []Track  `json:"tracklist"`
}

// Track is one track entry in a release or master.
type Track struct {
	Position string `json:"position"`
	Title    string `json:"title"`
	Duration string `json:"duration"`
}

// Master is a Discogs master release record.
type Master struct {
	ID      int      `kit:"id" json:"id"`
	Title   string   `json:"title"`
	Year    int      `json:"year"`
	Artists []string `json:"artists"`
	Genres  []string `json:"genres"`
	Styles  []string `json:"styles"`
}

// Label is a Discogs record label.
type Label struct {
	ID          int    `kit:"id" json:"id"`
	Name        string `json:"name"`
	Profile     string `json:"profile"`
	ContactInfo string `json:"contact_info"`
}

// --- Wire types (for JSON decode only) ---

type wireSearch struct {
	Results    []SearchResult `json:"results"`
	Pagination struct {
		Items int `json:"items"`
	} `json:"pagination"`
}

type wireArtistMember struct {
	Name string `json:"name"`
}

type wireArtist struct {
	ID             int                `json:"id"`
	Name           string             `json:"name"`
	Profile        string             `json:"profile"`
	URLs           []string           `json:"urls"`
	Namevariations []string           `json:"namevariations"`
	Members        []wireArtistMember `json:"members"`
}

type wireCredit struct {
	Name string `json:"name"`
}

type wireRelease struct {
	ID        int          `json:"id"`
	Title     string       `json:"title"`
	Year      int          `json:"year"`
	Artists   []wireCredit `json:"artists"`
	Genres    []string     `json:"genres"`
	Styles    []string     `json:"styles"`
	Tracklist []Track      `json:"tracklist"`
}

type wireMaster struct {
	ID        int          `json:"id"`
	Title     string       `json:"title"`
	Year      int          `json:"year"`
	Artists   []wireCredit `json:"artists"`
	Genres    []string     `json:"genres"`
	Styles    []string     `json:"styles"`
	Tracklist []Track      `json:"tracklist"`
}

type wireLabel struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Profile     string `json:"profile"`
	ContactInfo string `json:"contact_info"`
}

// --- API methods ---

// Search queries the Discogs database.
func (c *Client) Search(ctx context.Context, query, typ string, limit int) ([]*SearchResult, error) {
	u := BaseURL + "/database/search?q=" + urlEncode(query)
	if typ != "" && typ != "all" {
		u += "&type=" + urlEncode(typ)
	}
	if limit > 0 {
		u += "&per_page=" + strconv.Itoa(limit)
	}
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var w wireSearch
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	out := make([]*SearchResult, len(w.Results))
	for i := range w.Results {
		r := w.Results[i]
		out[i] = &r
	}
	return out, nil
}

// GetArtist fetches an artist by numeric ID.
func (c *Client) GetArtist(ctx context.Context, id string) (*Artist, error) {
	body, err := c.Get(ctx, BaseURL+"/artists/"+id)
	if err != nil {
		return nil, err
	}
	var w wireArtist
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("decode artist: %w", err)
	}
	members := make([]string, len(w.Members))
	for i, m := range w.Members {
		members[i] = m.Name
	}
	return &Artist{
		ID:      w.ID,
		Name:    w.Name,
		Profile: w.Profile,
		URLs:    w.URLs,
		Members: members,
	}, nil
}

// GetRelease fetches a release by numeric ID.
func (c *Client) GetRelease(ctx context.Context, id string) (*Release, error) {
	body, err := c.Get(ctx, BaseURL+"/releases/"+id)
	if err != nil {
		return nil, err
	}
	var w wireRelease
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	artists := make([]string, len(w.Artists))
	for i, a := range w.Artists {
		artists[i] = a.Name
	}
	return &Release{
		ID:        w.ID,
		Title:     w.Title,
		Year:      w.Year,
		Artists:   artists,
		Genres:    w.Genres,
		Styles:    w.Styles,
		Tracklist: w.Tracklist,
	}, nil
}

// GetMaster fetches a master release by numeric ID.
func (c *Client) GetMaster(ctx context.Context, id string) (*Master, error) {
	body, err := c.Get(ctx, BaseURL+"/masters/"+id)
	if err != nil {
		return nil, err
	}
	var w wireMaster
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("decode master: %w", err)
	}
	artists := make([]string, len(w.Artists))
	for i, a := range w.Artists {
		artists[i] = a.Name
	}
	return &Master{
		ID:      w.ID,
		Title:   w.Title,
		Year:    w.Year,
		Artists: artists,
		Genres:  w.Genres,
		Styles:  w.Styles,
	}, nil
}

// GetLabel fetches a record label by numeric ID.
func (c *Client) GetLabel(ctx context.Context, id string) (*Label, error) {
	body, err := c.Get(ctx, BaseURL+"/labels/"+id)
	if err != nil {
		return nil, err
	}
	var w wireLabel
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("decode label: %w", err)
	}
	return &Label{
		ID:          w.ID,
		Name:        w.Name,
		Profile:     w.Profile,
		ContactInfo: w.ContactInfo,
	}, nil
}

// urlEncode does a minimal percent-encoding for query string values.
func urlEncode(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		b := s[i]
		if isUnreserved(b) {
			out = append(out, b)
		} else if b == ' ' {
			out = append(out, '+')
		} else {
			out = append(out, '%', "0123456789ABCDEF"[b>>4], "0123456789ABCDEF"[b&0xf])
		}
	}
	return string(out)
}

func isUnreserved(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '~'
}
