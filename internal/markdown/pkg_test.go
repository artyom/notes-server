package markdown

import (
	"testing"

	gtext "github.com/yuin/goldmark/text"
	"golang.org/x/exp/slices"
)

func Test_slugify(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{"Hello, world", "hello-world"},
		{"multi    spaces", "multi-spaces"},
		{"- leading dash", "leading-dash"},
		{"Anyone there?!", "anyone-there"},
	} {
		if got := slugify(tc.input); got != tc.want {
			t.Fatalf("slugify(%q): got %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestAssignHeaderIDs(t *testing.T) {
	const bodyText = `
# Level One

Text

## Level two

Text
	`
	bodyBytes := []byte(bodyText)
	doc := Markdown.Parser().Parse(gtext.NewReader(bodyBytes))
	headers, err := AssignHeaderIDs(bodyBytes, doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []HeadingInfo{
		{Level: 1, Text: "Level One", Slug: "level-one"},
		{Level: 2, Text: "Level two", Slug: "level-two"},
	}
	if !slices.Equal(headers, want) {
		t.Fatalf("got headers:\n%+v\nwant:\n%+v", headers, want)
	}
}

func Test_nodeText(t *testing.T) {
	const input = `Some text, including an
[explicit](https://example.org),
and implicit links
https://example.org
.`
	const want = `Some text, including an explicit, and implicit links https://example.org.`
	bodyBytes := []byte(input)
	doc := Markdown.Parser().Parse(gtext.NewReader(bodyBytes))
	got := nodeText(doc, bodyBytes)
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestFirstParagraphText(t *testing.T) {
	for _, tc := range testCases {
		body := []byte(tc.body)
		doc := Markdown.Parser().Parse(gtext.NewReader(body))
		got := FirstParagraphText(body, doc)
		if got != tc.want {
			t.Fatalf("body:\n---\n%s\n---\ngot: %q\nwant: %q", tc.body, got, tc.want)
		}
	}
}

var testCases = []struct{ body, want string }{
	{
		body: `# Heading

Snippet.
`,
		want: `Snippet.`,
	},
	{
		body: `# Heading

<!--
 ignore -->

Snippet.`,
		want: `Snippet.`,
	},
	{
		body: `# Heading

<p>Nope.</p>

Snippet.`,
		want: "",
	},
	{
		body: `# Heading

1. List

Text
`,
		want: "",
	},
	{
		body: `# Heading

- List

Text
`,
		want: "",
	},
	{
		body: `# Heading

    #!/bin/sh
	echo "code block"

Text
`,
		want: "",
	},
	{
		body: `# Heading

> Quote

Text.
`,
		want: "",
	},
}
