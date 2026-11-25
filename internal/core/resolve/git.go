package resolve

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// GitImport is an import of a package from a git repository:
//
//	"git+https://github.com/acme/specs//heron@v1"      the newest v1.x tag
//	"git+https://github.com/acme/specs//heron@v1.2.0"  that tag
//
// After `git+` comes the repository's url; a `//` after the url's scheme
// starts the package's folder inside the repository, and the version after
// the last `@` is a major version or a tag. `heron run setup` pins the tag
// and its commit in heron.lock and installs the folder at that commit into
// .heron/packages/<Name>/, where the resolver finds it.
type GitImport struct {
	Spec  string // the import string as written
	URL   string // the repository, without `git+`
	Path  string // the package's folder inside the repository; "" for its root
	Ref   string // the tag asked for; "" for the newest tag of Major
	Major int
	// Name names the package: the url without its scheme, user or `.git`,
	// then its folder, as "github.com/acme/specs/heron". It is the lock's
	// key and the folder it is installed in under .heron/packages/.
	Name string
}

// PackagesDir is where `heron run setup` installs packages from git,
// relative to the repository's root.
const PackagesDir = ".heron/packages"

var tagRE = regexp.MustCompile(`^v?(\d+)(\.\d+)*$`)

// TagMajor returns the major version of a version tag such as "v1.2.0".
func TagMajor(tag string) (int, bool) {
	m := tagRE.FindStringSubmatch(tag)
	if m == nil {
		return 0, false
	}
	v, err := strconv.Atoi(m[1])
	return v, err == nil
}

// ParseGitImport reads a `git+…` import string.
func ParseGitImport(spec string) (GitImport, error) {
	g := GitImport{Spec: spec}
	rest, ok := strings.CutPrefix(spec, "git+")
	if !ok {
		return g, fmt.Errorf("%s: a git import starts with git+", spec)
	}
	at := strings.LastIndexByte(rest, '@')
	if at <= 0 || strings.Contains(rest[at:], "/") {
		return g, fmt.Errorf("%s: a git import names a major version or a tag, as in %s@v1", spec, spec)
	}
	version := rest[at+1:]
	rest = rest[:at]
	major, ok := TagMajor(version)
	if !ok {
		return g, fmt.Errorf("%s: the version after @ is a major version or a tag such as v1.2.0", spec)
	}
	g.Major = major
	if strings.Contains(version, ".") {
		g.Ref = version
	}
	scheme := strings.Index(rest, "://")
	if scheme < 0 {
		return g, fmt.Errorf("%s: a git import names the repository's url, as in git+https://host/repo//folder@v1", spec)
	}
	if i := strings.Index(rest[scheme+3:], "//"); i >= 0 {
		g.Path = strings.Trim(rest[scheme+3+i+2:], "/")
		rest = rest[:scheme+3+i]
	}
	g.URL = rest
	host := rest[scheme+3:]
	if i := strings.IndexByte(host, '@'); i >= 0 && i < strings.IndexByte(host+"/", '/') {
		host = host[i+1:] // user@host
	}
	host = strings.TrimSuffix(strings.Trim(host, "/"), ".git")
	name := path.Clean(path.Join(host, g.Path))
	if name == "." || strings.HasPrefix(name, "..") || strings.Contains(name, ":") {
		return g, fmt.Errorf("%s: can't name a package after this url", spec)
	}
	g.Name = name
	return g, nil
}

// InstallDir is where the package is installed under a repository's root.
func (g GitImport) InstallDir(root string) string {
	return filepath.Join(root, filepath.FromSlash(PackagesDir), filepath.FromSlash(g.Name))
}

// findGit finds an installed git package: under .heron/packages/ in the
// importing file's folder or a folder above it, or in a package directory.
func (r *Resolver) findGit(g GitImport, from *File) FileID {
	var dirs []string
	if from != nil && !from.ID.IsStd() {
		for d := from.ID.Dir(); ; {
			dirs = append(dirs, filepath.Join(d, filepath.FromSlash(PackagesDir)))
			up := filepath.Dir(d)
			if up == d {
				break
			}
			d = up
		}
	}
	dirs = append(dirs, r.PackageDirs...)
	for _, d := range dirs {
		for _, f := range []string{"package.heron", "index.heron"} {
			id, err := join(d, g.Name+"/"+f)
			if err == nil && r.Loader.exists(id) {
				return id
			}
		}
	}
	return ""
}
