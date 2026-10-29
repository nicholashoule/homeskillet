// Command site checks the website in site/ before it is published, for make
// site and the Pages workflow: the page exists, is bounded in size, declares a
// title, a language, and a viewport, every link or source it names relative
// to itself is a file under site/, and no link uses plain HTTP. The site is
// hand-written HTML with no build step, so this is the only check it gets.
//
// Usage: go run ./tools/site [DIR], where DIR defaults to site.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxPage bounds the page; a hand-written page is tens of kilobytes.
const maxPage = 4 << 20

func main() {
	dir := "site"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	problems, err := check(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "site:", err)
		os.Exit(2)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "site:", p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Printf("ok   %s: index.html and every file it references\n", dir)
}

// check returns the problems found with the site in dir, or an error when
// the page cannot be read at all.
func check(dir string) ([]string, error) {
	page := filepath.Join(dir, "index.html")
	info, err := os.Stat(page) //nolint:gosec // G703: the site directory named on the command line
	if err != nil {
		return nil, err
	}
	if info.Size() > maxPage {
		return nil, fmt.Errorf("%s is %d bytes, larger than %d", page, info.Size(), maxPage)
	}
	data, err := os.ReadFile(page) //nolint:gosec // G304: the site directory named on the command line
	if err != nil {
		return nil, err
	}
	html := string(data)
	var problems []string
	for _, required := range []struct{ marker, what string }{
		{"<title>", "a <title>"},
		{"<html lang=", "a lang attribute on <html>"},
		{`name="viewport"`, "a viewport meta tag, for phones"},
		{`name="description"`, "a description meta tag"},
	} {
		if !strings.Contains(html, required.marker) {
			problems = append(problems, "index.html lacks "+required.what)
		}
	}
	for _, ref := range references(html) {
		switch {
		case strings.HasPrefix(ref, "http://"):
			problems = append(problems, "plain HTTP link: "+ref)
		case strings.HasPrefix(ref, "https://"), strings.HasPrefix(ref, "#"), strings.HasPrefix(ref, "mailto:"), ref == "":
		case strings.HasPrefix(ref, "//") || strings.HasPrefix(ref, "/"):
			problems = append(problems, "absolute path, which breaks under a project page's prefix: "+ref)
		default:
			local, _, _ := strings.Cut(ref, "#")
			local, _, _ = strings.Cut(local, "?")
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(local))); errors.Is(err, os.ErrNotExist) { //nolint:gosec // G703: a reference from the page, looked up only under the site directory
				problems = append(problems, "references a file that is not in "+dir+": "+ref)
			} else if err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	return problems, nil
}

// references returns the value of every href and src attribute in html, in
// order. The page is hand-written, so a plain scan for the quoted attribute
// is enough; a parser would need a module outside the standard library.
func references(html string) []string {
	var refs []string
	for _, attribute := range []string{`href="`, `src="`} {
		rest := html
		for {
			i := strings.Index(rest, attribute)
			if i < 0 {
				break
			}
			rest = rest[i+len(attribute):]
			end := strings.IndexByte(rest, '"')
			if end < 0 {
				break
			}
			refs = append(refs, rest[:end])
			rest = rest[end:]
		}
	}
	return refs
}
