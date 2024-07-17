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

			package TEXT NOT NULL,

			FOREIGN KEY (npm_package) REFERENCES package(name)
		);
		CREATE TABLE IF NOT EXISTS package (
			name TEXT NOT NULL PRIMARY KEY,
		);
		CREATE TABLE IF NOT EXISTS package_version (
			package TEXT NOT NULL,
			version TEXT NOT NULL,

			released_at TEXT NOT NULL,
			published_by TEXT NOT NULL,
			contributors TEXT NOT NULL,

			num_direct_dependencies INTEGER NOT NULL,
			num_total_dependencies INTEGER NOT NULL,

			self_size_bytes INTEGER NOT NULL,
			total_size_bytes INTEGER NOT NULL,

			PRIMARY KEY (package, version),
			FOREIGN KEY (package) REFERENCES package(name)
		);
		CREATE TABLE IF NOT EXISTS package_version_downloads (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			package TEXT NOT NULL,
			version TEXT NOT NULL,

			date TEXT NOT NULL,
			weekly_downloads INTEGER NOT NULL, -- directly from npm
			daily_downloads INTEGER, -- null at first, before computing from adjacent weekly downloads

			FOREIGN KEY (package, version) REFERENCES package_version(package, version)
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
