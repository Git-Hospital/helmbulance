package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Rollback rolls release back to revision and waits, like an upgrade, until the release is ready.
// The revision is always named, never 0 ("the previous one"), so the run is the same on Helm 3
// and Helm 4 and the reproduce line says exactly what ran.
func (c *Client) Rollback(ctx context.Context, release, ns string, revision int) error {
	args := append([]string{"rollback", release, strconv.Itoa(revision)}, c.base(ns)...)
	args = append(args, c.waitArgs()...)
	_, err := c.run(ctx, args...)
	return err
}

// Revision is one row of helm history.
type Revision struct {
	Revision   int    `json:"revision"`
	Status     string `json:"status"`
	Chart      string `json:"chart"` // name-version, as helm prints it
	AppVersion string `json:"app_version"`
}

// History returns the release's revisions, oldest first.
func (c *Client) History(ctx context.Context, release, ns string) ([]Revision, error) {
	args := append([]string{"history", release, "-o", "json"}, c.base(ns)...)
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var revs []Revision
	if err := json.Unmarshal(out, &revs); err != nil {
		return nil, fmt.Errorf("parse the history of %s: %w", release, err)
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("helm gave no history for %s", release)
	}
	return revs, nil
}
