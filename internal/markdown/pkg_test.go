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
