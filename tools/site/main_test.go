// Tests of the site check: a sound page passes, and each kind of problem is
// reported.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sound := `<html lang="en"><head><title>x</title><meta name="viewport" content="w"><meta name="description" content="d"></head>` +
		`<body><a href="#top">t</a><a href="https://example.com/">e</a><img src="images/a.svg"><a href="images/a.svg#x">f</a></body></html>`
	write("index.html", sound)
	write("images/a.svg", "<svg/>")
	if problems, err := check(dir); err != nil || len(problems) != 0 {
		t.Fatalf("a sound site: %v, %v", problems, err)
	}

	write("index.html", `<html><head></head><body><a href="http://example.com/">p</a><img src="missing.png"><a href="/docs/">abs</a></body></html>`)
	problems, err := check(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lacks a <title>", "lacks a lang", "lacks a viewport", "lacks a description", "plain HTTP link", "not in " + dir + ": missing.png", "absolute path"} {
		if !strings.Contains(strings.Join(problems, "\n"), want) {
			t.Errorf("problems %q lack %q", problems, want)
		}
	}
	if _, err := check(filepath.Join(dir, "nowhere")); err == nil {
		t.Error("a missing site passed")
	}
}

func TestCheckReference(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "site")
	for _, name := range []string{filepath.Join(dir, "images", "a b.svg"), filepath.Join(dir, "index.html"), filepath.Join(root, "outside.txt")} {
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for ref, want := range map[string]string{
		"":                              "",
		"#install":                      "",
		"https://example.com/":          "",
		"mailto:someone@example.com":    "",
		"tel:+15550100":                 "",
		"data:image/svg+xml,%3Csvg/%3E": "",
		"images/a%20b.svg":              "",
		"images/a%20b.svg#frag":         "",
		"index.html?x=1":                "",
		"http://example.com/":           "plain HTTP",
		"HTTP://example.com/":           "plain HTTP",
		"javascript:alert(1)":           "javascript:",
		"//example.com/x.js":            "protocol-relative",
		"/docs/":                        "absolute path",
		"../outside.txt":                "outside",
		"images/../../outside.txt":      "outside",
		"images/missing.png":            "not in",
		"images/%zz.svg":                "percent-encoded",
	} {
		got := checkReference(dir, ref)
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("checkReference(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestReferences(t *testing.T) {
	html := `<a href="a.html"><img SRC='b.png'><link href=c.css><div data-src="not-me"></div>`
	got := strings.Join(references(html), " ")
	if want := "a.html c.css b.png"; got != want {
		t.Errorf("references = %q, want %q", got, want)
	}
}
