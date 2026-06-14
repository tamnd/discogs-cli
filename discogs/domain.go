package discogs

import (
	"context"
	"strconv"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go exposes the Discogs music database as a kit Domain: a driver that
// a multi-domain host (ant) enables with a single blank import,
//
//	import _ "github.com/tamnd/discogs-cli/discogs"
//
// exactly as a database/sql program enables a driver with `import _
// "github.com/lib/pq"`. The init below registers it; the host then dereferences
// discogs:// URIs by routing to the operations Register installs. The same
// Domain also builds the standalone discogs binary (see cli.NewApp), so the
// binary and a host share one source of truth.
func init() { kit.Register(Domain{}) }

// Domain is the Discogs driver. It carries no state; the per-run client is
// built by the factory Register hands kit.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against, and
// the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "discogs",
		Hosts:  []string{Host, "www.discogs.com", "discogs.com"},
		Identity: kit.Identity{
			Binary: "discogs",
			Short:  "Read public Discogs music database data",
			Long: `A command line for the Discogs music database.

discogs reads public Discogs data over HTTPS, shapes it into clean records,
and prints output that pipes into the rest of your tools. No API key required.`,
			Site: "discogs.com",
			Repo: "https://github.com/tamnd/discogs-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	kit.Handle(app, kit.OpMeta{Name: "search", Group: "read",
		Summary: "Search the Discogs database",
		Args:    []kit.Arg{{Name: "query", Help: "search terms"}}}, handleSearch)

	kit.Handle(app, kit.OpMeta{Name: "artist", Group: "read", Single: true,
		Summary: "Fetch an artist by id or URL", URIType: "artist", Resolver: true,
		Args: []kit.Arg{{Name: "id", Help: "artist id or URL"}}}, handleArtist)

	kit.Handle(app, kit.OpMeta{Name: "release", Group: "read", Single: true,
		Summary: "Fetch a release by id or URL", URIType: "release", Resolver: true,
		Args: []kit.Arg{{Name: "id", Help: "release id or URL"}}}, handleRelease)

	kit.Handle(app, kit.OpMeta{Name: "master", Group: "read", Single: true,
		Summary: "Fetch a master release by id or URL", URIType: "master", Resolver: true,
		Args: []kit.Arg{{Name: "id", Help: "master id or URL"}}}, handleMaster)

	kit.Handle(app, kit.OpMeta{Name: "label", Group: "read", Single: true,
		Summary: "Fetch a record label by id or URL", URIType: "label", Resolver: true,
		Args: []kit.Arg{{Name: "id", Help: "label id or URL"}}}, handleLabel)
}

// newClient builds the client from the host-resolved config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := NewClient()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.HTTP.Timeout = cfg.Timeout
	}
	return c, nil
}

// --- Input structs ---

type searchInput struct {
	Query  string  `kit:"arg" help:"search terms"`
	Type   string  `kit:"flag" help:"artist|release|master|label|all"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type artistInput struct {
	ID     string  `kit:"arg" help:"artist id or URL"`
	Client *Client `kit:"inject"`
}

type releaseInput struct {
	ID     string  `kit:"arg" help:"release id or URL"`
	Client *Client `kit:"inject"`
}

type masterInput struct {
	ID     string  `kit:"arg" help:"master id or URL"`
	Client *Client `kit:"inject"`
}

type labelInput struct {
	ID     string  `kit:"arg" help:"label id or URL"`
	Client *Client `kit:"inject"`
}

// --- Handlers ---

func handleSearch(ctx context.Context, in searchInput, emit func(*SearchResult) error) error {
	results, err := in.Client.Search(ctx, in.Query, in.Type, in.Limit)
	if err != nil {
		return mapErr(err)
	}
	for _, r := range results {
		if err := emit(r); err != nil {
			return err
		}
	}
	return nil
}

func handleArtist(ctx context.Context, in artistInput, emit func(*Artist) error) error {
	id := extractID(in.ID, "artists")
	a, err := in.Client.GetArtist(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	return emit(a)
}

func handleRelease(ctx context.Context, in releaseInput, emit func(*Release) error) error {
	id := extractID(in.ID, "releases")
	r, err := in.Client.GetRelease(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	return emit(r)
}

func handleMaster(ctx context.Context, in masterInput, emit func(*Master) error) error {
	id := extractID(in.ID, "masters")
	m, err := in.Client.GetMaster(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	return emit(m)
}

func handleLabel(ctx context.Context, in labelInput, emit func(*Label) error) error {
	id := extractID(in.ID, "labels")
	l, err := in.Client.GetLabel(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	return emit(l)
}

// --- Resolver: pure string functions, no network ---

// Classify turns any accepted input into (uriType, id).
// - Numeric ID → default "release" type
// - URL path like /artists/45 → parse type+ID
// - Non-numeric, non-URL → "search" type
func (Domain) Classify(input string) (uriType, id string, err error) {
	input = strings.TrimSpace(input)

	// Strip scheme and host if it's a full URL.
	path := input
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		// Find path portion.
		after := input
		if i := strings.Index(after, "://"); i >= 0 {
			after = after[i+3:]
		}
		if i := strings.Index(after, "/"); i >= 0 {
			path = after[i:]
		} else {
			path = "/"
		}
	}

	path = strings.Trim(path, "/")

	// Path like "artists/45", "releases/249504", "masters/56597", "labels/1".
	if parts := strings.SplitN(path, "/", 2); len(parts) == 2 {
		segment := strings.ToLower(parts[0])
		idPart := parts[1]
		switch segment {
		case "artist", "artists":
			return "artist", idPart, nil
		case "release", "releases":
			return "release", idPart, nil
		case "master", "masters":
			return "master", idPart, nil
		case "label", "labels":
			return "label", idPart, nil
		}
	}

	// Bare numeric ID → default to release.
	if _, err := strconv.Atoi(path); err == nil && path != "" {
		return "release", path, nil
	}

	// Non-numeric, non-URL → treat as search query.
	if path != "" {
		return "search", path, nil
	}

	return "", "", errs.Usage("unrecognized Discogs reference: %q", input)
}

// Locate returns the canonical https URL for a (type, id).
func (Domain) Locate(uriType, id string) (string, error) {
	switch uriType {
	case "artist":
		return "https://www.discogs.com/artist/" + id, nil
	case "release":
		return "https://www.discogs.com/release/" + id, nil
	case "master":
		return "https://www.discogs.com/master/" + id, nil
	case "label":
		return "https://www.discogs.com/label/" + id, nil
	case "search":
		return "https://www.discogs.com/search?q=" + urlEncode(id), nil
	}
	return "", errs.Usage("discogs has no resource type %q", uriType)
}

// --- helpers ---

// extractID pulls the numeric id from a raw arg, which may be:
// - a bare number ("45")
// - a URL path segment like "/artists/45"
// - a full URL like "https://www.discogs.com/artist/45"
func extractID(input, segment string) string {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		if i := strings.LastIndex(input, "/"); i >= 0 {
			return input[i+1:]
		}
	}
	// Path like "artists/45" or "/artists/45".
	input = strings.Trim(input, "/")
	if parts := strings.SplitN(input, "/", 2); len(parts) == 2 {
		return parts[1]
	}
	return input
}

// mapErr converts a library error into the appropriate kit error kind.
func mapErr(err error) error {
	return err
}
