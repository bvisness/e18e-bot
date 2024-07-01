package e18e

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/bvisness/e18e-bot/config"
	"github.com/bvisness/e18e-bot/utils"
	_ "github.com/mattn/go-sqlite3"
)

var conn *sql.DB

func OpenDB() {
	conn = utils.Must1(sql.Open("sqlite3", config.Config.Db.DSN))
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

type SQLiteTime time.Time

func (s *SQLiteTime) Scan(src any) error {
	switch src.(type) {
	case string:
		t, err := time.Parse("2006-01-02 15:04:05-07:00", src.(string))
		if err != nil {
			return fmt.Errorf("failed to parse ISO time: %w", err)
		}
		*s = SQLiteTime(t)
		return nil
	default:
		return fmt.Errorf("failed to parse %v (%#v) as a SQLite time", src, src)
	}
}

func (s SQLiteTime) String() string {
	return time.Time(s).String()
}
