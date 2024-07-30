package e18e

import (
	"log/slog"
	"os"

	"github.com/bvisness/e18e-bot/utils"
)

type TmpDir string

func MakeTmpDir(pattern string) TmpDir {
	return TmpDir(utils.Must1(os.MkdirTemp("", pattern)))
}

func (t TmpDir) Remove() {
	if t != "" {
		err := os.RemoveAll(string(t))
		if err != nil {
			slog.Warn("Failed to delete tmpdir", "path", t)
		}
	}
}
