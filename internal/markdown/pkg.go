package markdown

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var Markdown = goldmark.New(
	goldmark.WithRendererOptions(html.WithUnsafe()),
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

type HeadingInfo struct {
	Text, Slug string
	Level      int
}

func AssignHeaderIDs(body []byte, doc ast.Node) ([]HeadingInfo, error) {
	var headers []HeadingInfo
	var seen map[string]struct{} // keeps track of seen slugs to avoid duplicate ids
	fn := func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		kind := n.Kind()
		if entering && kind == ast.KindParagraph {
			return ast.WalkSkipChildren, nil
		}
		if !entering || kind != ast.KindHeading {
			return ast.WalkContinue, nil
		}
		hdr := HeadingInfo{Text: nodeText(n, body)}
		if h, ok := n.(*ast.Heading); ok {
			hdr.Level = h.Level
		}
		if name := slugify(hdr.Text); name != "" {
			if seen == nil {
				seen = make(map[string]struct{})
			}
			for i := 0; i < 100; i++ {
				var cand string
				if i == 0 {
					cand = name
				} else {
					cand = fmt.Sprintf("%s-%d", name, i)
				}
				if _, ok := seen[cand]; !ok {
					seen[cand] = struct{}{}
					n.SetAttributeString("id", []byte(cand))
					hdr.Slug = cand
					headers = append(headers, hdr)
					break
				}
			}
		}
		return ast.WalkContinue, nil
	}
	if err := ast.Walk(doc, fn); err != nil {
		return nil, err
	}
	return headers, nil
}

// nodeText walks node and extracts plain text from it and its descendants,
// effectively removing all markdown syntax
func nodeText(node ast.Node, src []byte) string {
	var b strings.Builder
	fn := func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindText:
			if t, ok := n.(*ast.Text); ok {
				b.Write(t.Text(src))
			}
		}
		return ast.WalkContinue, nil
	}
	if err := ast.Walk(node, fn); err != nil {
		return ""
	}
	return b.String()
}

func slugify(text string) string {
	var hasRunes bool
	var prevDash bool
	fn := func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			hasRunes = true
			prevDash = false
			return unicode.ToLower(r)
		}
		if hasRunes && !prevDash {
			prevDash = true
			return '-'
		}
		return -1
	}
	return strings.TrimRight(strings.Map(fn, text), "-")
}

func WordCountAtLeast(text []byte, want int) bool {
	scanner := bufio.NewScanner(bytes.NewReader(text))
	scanner.Split(bufio.ScanWords)
	var cnt int
	for scanner.Scan() {
		cnt++
		if cnt == want {
			return true
		}
	}
	_ = scanner.Err()
	return cnt >= want
}

// FirstParagraphText returns the plain text of the first paragraph in a
// document. If there's anything but headings or html comments before such a
// paragraph, it returns an empty string.
func FirstParagraphText(body []byte, doc ast.Node) string {
	var text string
	fn := func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		kind := n.Kind()
		switch kind {
		case ast.KindDocument:
			return ast.WalkContinue, nil
		case ast.KindHeading:
			return ast.WalkSkipChildren, nil
		case ast.KindParagraph:
			text = nodeText(n, body)
			return ast.WalkStop, nil
		case ast.KindHTMLBlock:
			if lines := n.Lines(); lines != nil {
				s := lines.At(0)
				if bytes.HasPrefix(body[s.Start:s.Stop], []byte("<!--")) {
					return ast.WalkContinue, nil
				}
			}
		}
		return ast.WalkStop, nil
	}
	_ = ast.Walk(doc, fn)
	return strings.TrimSpace(text)
}
