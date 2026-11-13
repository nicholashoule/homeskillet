// Command site checks the website in site/ before it is published, for make
// site and the Pages workflow: the page exists, is bounded in size, declares a
// title, a language, a viewport, a description, a canonical URL, and the Open
// Graph title and description that link previews show, matching the title and
// description search results show; any structured data is valid JSON; every
// link or source it names relative to itself is a file inside site/; and no
// link uses plain HTTP or runs script. The site is hand-written HTML with no
// build step, so this is the only check it gets. It is a development tool, not
// linked into homeskillet, so it may use net/url and encoding/json.
//
// Usage: go run ./tools/site [DIR], where DIR defaults to site.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
		// Pages serves the page at both / and /index.html; without a
		// canonical URL a search engine may index them as two pages.
		{`rel="canonical"`, "a canonical link, so / and /index.html count as one page"},
	} {
		if !strings.Contains(html, required.marker) {
			problems = append(problems, "index.html lacks "+required.what)
		}
	}
	problems = append(problems, checkMeta(html)...)
	problems = append(problems, checkStructuredData(html)...)
	for _, ref := range references(html) {
		if problem := checkReference(dir, ref); problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems, nil
}

// checkMeta returns the problems with the page's <meta> tags. Phones need the
// viewport, search results show the description, and link previews in chat
// and social apps show the Open Graph title and description. Those repeat the
// <title> and description by hand, so they must match them, or a shared link
// would describe the page differently from a search result.
func checkMeta(html string) []string {
	metas := metaContents(html)
	var problems []string
	for _, required := range []struct{ key, what string }{
		{"viewport", "a viewport meta tag, for phones"},
		{"description", "a description meta tag, which search results show"},
		{"og:title", "an og:title meta tag, which link previews show"},
		{"og:description", "an og:description meta tag, which link previews show"},
	} {
		if strings.TrimSpace(metas[required.key]) == "" {
			problems = append(problems, "index.html lacks "+required.what)
		}
	}
	if title, og := elementText(html, "title"), metas["og:title"]; title != "" && og != "" && og != title {
		problems = append(problems, fmt.Sprintf("og:title %q differs from the <title> %q", og, title))
	}
	if description, og := metas["description"], metas["og:description"]; description != "" && og != "" && og != description {
		problems = append(problems, fmt.Sprintf("og:description %q differs from the description %q", og, description))
	}
	return problems
}

// checkStructuredData returns a problem for each application/ld+json script
// that is not valid JSON. A search engine skips such a block without saying
// so, so this check is the only place the mistake would show.
func checkStructuredData(html string) []string {
	lower := strings.ToLower(html)
	var problems []string
	for at := 0; ; {
		i := strings.Index(lower[at:], "<script")
		if i < 0 {
			return problems
		}
		attrs, end := tagAttributes(html, at+i+len("<script"))
		if end >= len(html) {
			return append(problems, "index.html ends inside a <script> tag")
		}
		body := end + 1
		length := strings.Index(lower[body:], "</script")
		if length < 0 {
			return append(problems, "index.html has a <script> that is never closed")
		}
		at = body + length
		if strings.EqualFold(strings.TrimSpace(attrs["type"]), "application/ld+json") && !json.Valid([]byte(html[body:at])) {
			problems = append(problems, "structured data in an application/ld+json script is not valid JSON")
		}
	}
}

// checkReference returns the problem with one reference from the page, or
// "". Links to other sites must use HTTPS. The schemes that can carry script
// are refused: javascript: and vbscript: run it when followed, and a data: URL
// runs it as a script's or a frame's source; anything the page embeds can be
// a file in dir instead. Any other URL scheme, such as mailto:, is left alone.
// Everything else names a file, which must be inside dir, since only dir is
// published, and must exist.
func checkReference(dir, ref string) string {
	ref = strings.TrimSpace(ref)
	lower := strings.ToLower(ref)
	switch {
	case ref == "", strings.HasPrefix(ref, "#"):
		return ""
	case strings.HasPrefix(lower, "http:"):
		return "plain HTTP link: " + ref
	case strings.HasPrefix(lower, "javascript:"), strings.HasPrefix(lower, "vbscript:"):
		return "script link, which runs code when followed: " + ref
	case strings.HasPrefix(lower, "data:"):
		return "data: URL, which can carry script; make it a file in " + dir + ": " + ref
	case strings.HasPrefix(ref, "//"):
		return "protocol-relative link, which may load over plain HTTP: " + ref
	case hasScheme(ref):
		return ""
	case strings.HasPrefix(ref, "/"):
		return "absolute path, which breaks under a project page's prefix: " + ref
	}
	local, _, _ := strings.Cut(ref, "#")
	local, _, _ = strings.Cut(local, "?")
	decoded, err := url.PathUnescape(local)
	if err != nil {
		return "badly percent-encoded reference: " + ref
	}
	path := filepath.Join(dir, filepath.FromSlash(decoded))
	if rel, err := filepath.Rel(dir, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "references a file outside " + dir + ", which is not published: " + ref
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) { //nolint:gosec // G703: a reference from the page, checked above to stay under the site directory
		return "references a file that is not in " + dir + ": " + ref
	} else if err != nil {
		return err.Error()
	}
	return ""
}

// hasScheme reports whether ref starts with a URL scheme, such as https: or
// mailto:: a letter, then letters, digits, +, -, or ., then a colon, all before
// any /, ?, or #.
func hasScheme(ref string) bool {
	for i, r := range ref {
		switch {
		case r == ':':
			return i > 0
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z':
		case i > 0 && ('0' <= r && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return false
}

// references returns the value of every href and src attribute in html:
// written in any case, quoted with either quote or not at all. The page is
// hand-written, so a scan for the attribute is enough; an HTML parser would
// need a module outside the standard library. An attribute counts only after
// whitespace, so data-src is not src.
func references(html string) []string {
	lower := strings.ToLower(html)
	var refs []string
	for _, name := range []string{"href=", "src="} {
		for at := 0; ; {
			i := strings.Index(lower[at:], name)
			if i < 0 {
				break
			}
			start := at + i + len(name)
			at = start
			if before := at - len(name) - 1; before < 0 || !isSpace(html[before]) {
				continue
			}
			value, next := attributeValue(html, start)
			refs = append(refs, value)
			at = next
		}
	}
	return refs
}

// metaContents returns the content of each <meta> tag in html, keyed by its
// name attribute, or for Open Graph's tags its property attribute, in lower
// case.
func metaContents(html string) map[string]string {
	lower := strings.ToLower(html)
	metas := map[string]string{}
	for at := 0; ; {
		i := strings.Index(lower[at:], "<meta")
		if i < 0 {
			return metas
		}
		attrs, end := tagAttributes(html, at+i+len("<meta"))
		at = end
		key := attrs["name"]
		if key == "" {
			key = attrs["property"]
		}
		if key != "" {
			metas[strings.ToLower(key)] = attrs["content"]
		}
	}
}

// elementText returns the text between the first <name> and </name> in
// html, trimmed, or "" when there is none.
func elementText(html, name string) string {
	lower := strings.ToLower(html)
	open, closing := "<"+name+">", "</"+name+">"
	start := strings.Index(lower, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	length := strings.Index(lower[start:], closing)
	if length < 0 {
		return ""
	}
	return strings.TrimSpace(html[start : start+length])
}

// tagAttributes reads a tag's attributes from html[start], just past the
// tag's name, up to the > that ends it, and returns them by name in lower
// case, along with the index of that > (or len(html) when there is none). A >
// inside a quoted value does not end the tag. Every pass of the loop moves
// forward, so a malformed tag cannot stall it.
func tagAttributes(html string, start int) (map[string]string, int) {
	attrs := map[string]string{}
	i := start
	for i < len(html) && html[i] != '>' {
		if isSpace(html[i]) || html[i] == '/' {
			i++
			continue
		}
		nameStart := i
		for i < len(html) && !isSpace(html[i]) && html[i] != '=' && html[i] != '>' && html[i] != '/' {
			i++
		}
		name := strings.ToLower(html[nameStart:i])
		for i < len(html) && isSpace(html[i]) {
			i++
		}
		if i >= len(html) || html[i] != '=' {
			attrs[name] = ""
			continue
		}
		i++
		for i < len(html) && isSpace(html[i]) {
			i++
		}
		attrs[name], i = attributeValue(html, i)
	}
	return attrs, i
}

// attributeValue returns the attribute value that starts at html[start],
// quoted or not, and where scanning resumes.
func attributeValue(html string, start int) (string, int) {
	if start >= len(html) {
		return "", start
	}
	if quote := html[start]; quote == '"' || quote == '\'' {
		end := strings.IndexByte(html[start+1:], quote)
		if end < 0 {
			return html[start+1:], len(html)
		}
		return html[start+1 : start+1+end], start + 2 + end
	}
	end := start
	for end < len(html) && !isSpace(html[end]) && html[end] != '>' {
		end++
	}
	return html[start:end], end
}

// isSpace reports whether b is HTML whitespace.
func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}
