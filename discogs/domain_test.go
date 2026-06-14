package discogs

import (
	"testing"
)

// These tests are offline: they exercise the URI driver's pure string functions
// and the domain info. The client's HTTP behaviour is covered in discogs_test.go.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "discogs" {
		t.Errorf("Scheme = %q, want discogs", info.Scheme)
	}
	if len(info.Hosts) == 0 {
		t.Error("Hosts is empty")
	}
	found := false
	for _, h := range info.Hosts {
		if h == Host {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Hosts = %v, want to contain %s", info.Hosts, Host)
	}
	if info.Identity.Binary != "discogs" {
		t.Errorf("Identity.Binary = %q, want discogs", info.Identity.Binary)
	}
}

func TestClassify(t *testing.T) {
	d := Domain{}
	cases := []struct {
		in      string
		wantTyp string
		wantID  string
		wantErr bool
	}{
		// Numeric ID defaults to release.
		{"249504", "release", "249504", false},
		// URL path segment with type.
		{"/artists/45", "artist", "45", false},
		{"artists/45", "artist", "45", false},
		{"/releases/249504", "release", "249504", false},
		{"/masters/56597", "master", "56597", false},
		{"/labels/1", "label", "1", false},
		// Full Discogs URL.
		{"https://www.discogs.com/artist/45", "artist", "45", false},
		{"https://www.discogs.com/release/249504", "release", "249504", false},
		{"https://www.discogs.com/master/56597", "master", "56597", false},
		{"https://www.discogs.com/label/1", "label", "1", false},
		// API URL.
		{"https://api.discogs.com/artists/45", "artist", "45", false},
		// Non-numeric, non-URL → search.
		{"radiohead", "search", "radiohead", false},
		// Empty → error.
		{"", "", "", true},
	}
	for _, tc := range cases {
		typ, id, err := d.Classify(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Classify(%q) = (%q, %q, nil), want error", tc.in, typ, id)
			}
			continue
		}
		if err != nil || typ != tc.wantTyp || id != tc.wantID {
			t.Errorf("Classify(%q) = (%q, %q, %v), want (%q, %q, nil)",
				tc.in, typ, id, err, tc.wantTyp, tc.wantID)
		}
	}
}

func TestLocate(t *testing.T) {
	d := Domain{}
	cases := []struct {
		typ  string
		id   string
		want string
	}{
		{"artist", "45", "https://www.discogs.com/artist/45"},
		{"release", "249504", "https://www.discogs.com/release/249504"},
		{"master", "56597", "https://www.discogs.com/master/56597"},
		{"label", "1", "https://www.discogs.com/label/1"},
	}
	for _, tc := range cases {
		got, err := d.Locate(tc.typ, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("Locate(%q, %q) = (%q, %v), want (%q, nil)",
				tc.typ, tc.id, got, err, tc.want)
		}
	}

	// Unknown type returns error.
	if _, err := d.Locate("unknown", "123"); err == nil {
		t.Error("Locate(unknown, ...) = nil error, want error")
	}
}
