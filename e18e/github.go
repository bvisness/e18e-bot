package e18e

import (
	"github.com/google/go-github/v62/github"
)

var githubClient = github.NewClient(nil)

func IsGitHubResponse(err error, status int) bool {
	if res, ok := err.(*github.ErrorResponse); ok {
		return res.Response.StatusCode == status
	}
	return false
}
