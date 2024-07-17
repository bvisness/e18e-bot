package e18e

import "fmt"

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
