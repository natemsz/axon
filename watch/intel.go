package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The intel windows' data (package gui, intel.go): the site's intel
// API, read with this computer's signed credential, so Axon needs no
// site sign-in. The answers pass through to the windows' pages as
// the site wrote them; the pages read the fields they draw.

// IntelThreads is the workbench's thread rows.
func (c *Client) IntelThreads(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, "/api/intel/v1/threads", nil, &out)
	return out, err
}

// IntelFeed is the activity log, newer than report after.
func (c *Client) IntelFeed(ctx context.Context, after int64) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/intel/v1/feed?after=%d", after), nil, &out)
	return out, err
}

// IntelReport is one report broken down, as the workbench shows it.
func (c *Client) IntelReport(ctx context.Context, id int64) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/intel/v1/reports/%d", id), nil, &out)
	return out, err
}

// Universe is the site's public map payload (/map/universe.json): every
// system with its improved-projection position, and the gates. It is
// public, so it is fetched plainly, not signed.
func (c *Client) Universe(ctx context.Context) ([]byte, error) {
	u, err := url.JoinPath(strings.TrimRight(c.Server, "/"), "/map/universe.json")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.Agent != "" {
		req.Header.Set("User-Agent", c.Agent)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("the site's map is not available (" + res.Status + ")")
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}
