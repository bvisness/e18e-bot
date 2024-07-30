package npm

import (
	"fmt"
	"time"
)

type Package struct {
	Name        string                     `json:"name"`
	Maintainers []User                     `json:"maintainers"`
	Versions    map[string]*PackageVersion `json:"versions"`
	Times       map[string]string          `json:"time"`
}

func (p *Package) Url() string {
	return fmt.Sprintf("https://www.npmjs.com/package/%s", p.Name)
}

type PackageVersion struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      User   `json:"author"`
	Maintainers []User `json:"maintainers"`
	Publisher   User   `json:"_npmUser"`

	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`

	// May be filled in by the `time` field when querying packages.
	PublishedAt time.Time
}

type User struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type PackageLockFile struct {
	Name            string `json:"name"`
	LockfileVersion int    `json:"lockfileVersion"`

	Packages map[string]PackageLockFilePackage `json:"packages"`
}

type PackageLockFilePackage struct {
	Version  string `json:"version"`
	Resolved string `json:"resolved"`
}
