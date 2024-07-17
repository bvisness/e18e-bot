package e18e

import (
	"fmt"
	"time"
)

type PR struct {
	ID int `db:"id"`

	Owner      string     `db:"owner"`
	Repo       string     `db:"repo"`
	PullNumber int        `db:"pull_number"`
	OpenedAt   SQLiteTime `db:"opened_at"`

	Package string `db:"package"`
}

func (pr *PR) Url() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.Owner, pr.Repo, pr.PullNumber)
}

type Package struct {
	Name string `db:"name"`
}

func (p *Package) Url() string {
	return fmt.Sprintf("https://www.npmjs.com/package/%s", p.Name)
}

type PackageVersion struct {
	Package string `db:"package"`
	Version string `db:"version"`

	ReleasedAt  time.Time `db:"released_at"`
	PublishedBy string    `db:"published_by"`
	// TODO: Handle string arrays through Scanner
	// Maintainers []string  `db:"maintainers"`

	StatsComputed                bool `db:"stats_computed"`
	NumDirectDependencies        int  `db:"num_direct_dependencies"`
	NumTransitiveDependencies    int  `db:"num_transitive_dependencies"`
	NumDirectDevDependencies     int  `db:"num_direct_dev_dependencies"`
	NumTransitiveDevDependencies int  `db:"num_transitive_dev_dependencies"`
	SelfSizeBytes                int  `db:"self_size_bytes"`
	TransitiveSizeBytes          int  `db:"transitive_size_bytes"`
	SelfSizeDevBytes             int  `db:"self_size_dev_bytes"`
	TransitiveSizeDevBytes       int  `db:"transitive_size_dev_bytes"`
}

type PackageVersionDownloads struct {
	ID      int    `db:"id"`
	Package string `db:"package"`
	Version string `db:"version"`

	Date            time.Time `db:"date"`
	WeeklyDownloads int       `db:"weekly_downloads"`
	DailyDownloads  *int      `db:"daily_downloads"`
}
