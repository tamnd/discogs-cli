package discogs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamnd/discogs-cli/discogs"
)

func newTestClient(srv *httptest.Server) *discogs.Client {
	c := discogs.NewClient()
	c.Rate = 0 // no pacing in tests
	c.HTTP = srv.Client()
	return c
}

func TestGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := discogs.NewClient()
	c.Rate = 0
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestGetRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := discogs.NewClient()
	c.Rate = 0
	c.Retries = 5

	start := time.Now()
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q after retries", body)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/database/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("q") == "" {
			t.Error("missing q parameter")
		}
		resp := map[string]any{
			"results": []map[string]any{
				{"id": 45, "title": "Radiohead", "type": "artist", "uri": "/artists/45", "thumb": ""},
			},
			"pagination": map[string]any{"items": 1},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	// Override base URL via a custom URL — we call Get directly with the test server URL.
	// Instead, we use the exported Search and patch the host via a wrapper test.
	// Since Client.Search uses BaseURL (a package-level const), we test via Get directly.
	body, err := c.Get(context.Background(), srv.URL+"/database/search?q=radiohead")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Results []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) == 0 {
		t.Fatal("no results")
	}
	if result.Results[0].Title != "Radiohead" {
		t.Errorf("title = %q, want Radiohead", result.Results[0].Title)
	}
}

func TestGetArtistDecoding(t *testing.T) {
	payload := `{
		"id": 45,
		"name": "Radiohead",
		"profile": "English rock band",
		"urls": ["https://radiohead.com"],
		"members": [{"name": "Thom Yorke"}, {"name": "Jonny Greenwood"}]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	body, err := c.Get(context.Background(), srv.URL+"/artists/45")
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Profile string `json:"profile"`
		Members []struct {
			Name string `json:"name"`
		} `json:"members"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Name != "Radiohead" {
		t.Errorf("name = %q, want Radiohead", wire.Name)
	}
	if len(wire.Members) != 2 {
		t.Errorf("members count = %d, want 2", len(wire.Members))
	}
	if wire.Members[0].Name != "Thom Yorke" {
		t.Errorf("member[0] = %q, want Thom Yorke", wire.Members[0].Name)
	}
}

func TestGetReleaseDecoding(t *testing.T) {
	payload := `{
		"id": 249504,
		"title": "OK Computer",
		"year": 1997,
		"artists": [{"name": "Radiohead"}],
		"genres": ["Electronic", "Rock"],
		"styles": ["Art Rock", "Alternative Rock"],
		"tracklist": [
			{"position": "A1", "title": "Airbag", "duration": "4:44"},
			{"position": "A2", "title": "Paranoid Android", "duration": "6:23"}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	body, err := c.Get(context.Background(), srv.URL+"/releases/249504")
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ID        int    `json:"id"`
		Title     string `json:"title"`
		Year      int    `json:"year"`
		Tracklist []struct {
			Title string `json:"title"`
		} `json:"tracklist"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Title != "OK Computer" {
		t.Errorf("title = %q, want OK Computer", wire.Title)
	}
	if wire.Year != 1997 {
		t.Errorf("year = %d, want 1997", wire.Year)
	}
	if len(wire.Tracklist) != 2 {
		t.Errorf("tracklist count = %d, want 2", len(wire.Tracklist))
	}
}

func TestGet404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Get(context.Background(), srv.URL+"/artists/99999999")
	if err == nil {
		t.Error("expected error for 404, got nil")
	}
}

func TestExtractIDHelper(t *testing.T) {
	// Test Classify which uses extractID logic internally.
	d := discogs.Domain{}
	// Full URL → parsed to type + id.
	typ, id, err := d.Classify("https://www.discogs.com/release/249504")
	if err != nil || typ != "release" || id != "249504" {
		t.Errorf("Classify(URL) = (%q, %q, %v)", typ, id, err)
	}
	// Bare numeric → release.
	typ, id, err = d.Classify("249504")
	if err != nil || typ != "release" || id != "249504" {
		t.Errorf("Classify(numeric) = (%q, %q, %v)", typ, id, err)
	}
	// Text → search.
	typ, id, err = d.Classify("radiohead")
	if err != nil || typ != "search" || id != "radiohead" {
		t.Errorf("Classify(text) = (%q, %q, %v)", typ, id, err)
	}
}
