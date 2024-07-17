package npm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bvisness/e18e-bot/utils"
)

type Client struct {
	C *http.Client
}

var ErrNotFound = errors.New("not found")

func (c *Client) GetPackage(name string) (Package, error) {
	res, err := c.C.Get(fmt.Sprintf("https://registry.npmjs.org/%s", name))
	if err != nil {
		return Package{}, fmt.Errorf("failed to fetch npm package: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return Package{}, ErrNotFound
	}

	var p Package
	utils.Must(json.Unmarshal(utils.Must1(io.ReadAll(res.Body)), &p))

	// Set the PublishedAt field on versions
	for _, v := range p.Versions {
		v.PublishedAt = utils.Must1(time.Parse(time.RFC3339, p.Times[v.Version]))
	}

	return p, nil
}
