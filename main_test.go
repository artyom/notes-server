package main

import (
	"testing"

	gtext "github.com/yuin/goldmark/text"
	"golang.org/x/exp/slices"
)

func Test_noteTags(t *testing.T) {
	const text = `
	<!-- Tags: tag2,tag1,  tag2, tag3,	-->
	<!--
		No tags here
	-->`
	want := []string{"tag2", "tag1", "tag3"}
	got := noteTags(text)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func Benchmark_noteTags(b *testing.B) {
	const text = `
	<!-- Tags: tag1, tag2, tag3,	-->
	<!--
		No tags here
	-->`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = noteTags(text)
	}
}

var sink []string

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

func Test_assignHeaderIDs(t *testing.T) {
	const bodyText = `
# Level One

Text

## Level two

Text
	`
	bodyBytes := []byte(bodyText)
	doc := markdown.Parser().Parse(gtext.NewReader(bodyBytes))
	headers, err := assignHeaderIDs(bodyBytes, doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []headingInfo{
		{Level: 1, Text: "Level One", Slug: "level-one"},
		{Level: 2, Text: "Level two", Slug: "level-two"},
	}
	if !slices.Equal(headers, want) {
		t.Fatalf("got headers:\n%+v\nwant:\n%+v", headers, want)
	}
}
