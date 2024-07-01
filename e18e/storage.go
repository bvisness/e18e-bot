package e18e

import (
	"database/sql"
	"time"

	"github.com/bvisness/e18e-bot/config"
	"github.com/bvisness/e18e-bot/utils"
	_ "github.com/mattn/go-sqlite3"
)

var conn *sql.DB

func OpenDB() {
	conn = utils.Must1(sql.Open("sqlite3", config.Config.Db.DSN))
}

type PR struct {
	ID int `db:"id"`

	Owner      string    `db:"owner"`
	Repo       string    `db:"repo"`
	PullNumber int       `db:"pull_number"`
	OpenedAt   time.Time `db:"opened_at"`

	NPMPackage string `db:"npm_package"`
}

func MigrateDB() {
	utils.Must1(conn.Exec(`
		CREATE TABLE IF NOT EXISTS pr (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,

			owner TEXT NOT NULL,
			repo TEXT NOT NULL,
			pull_number INTEGER NOT NULL,
			opened_at TEXT NOT NULL,

			npm_package TEXT NOT NULL
		);
	`))
}
