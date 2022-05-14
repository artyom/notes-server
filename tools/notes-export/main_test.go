package main

import (
	"testing"
)

func Test_atomFeed(t *testing.T) {
	testCases := [...]struct {
		body     string
		wantFeed bool
	}{
		{
			body: `<!doctype html><head><meta charset="utf-8"><title>Nope</title><head>Body`,
		},
		{
			body: `<!doctype html><head><meta charset="utf-8"><title>Fallback title</title>
			<link rel="alternate" title="Title from the link" type="application/atom+xml" href="/feed.atom">
			</head>Body
			`,
			wantFeed: true,
		},
		{
			body: `<!doctype html><head><meta charset="utf-8"><title>Fallback title</title>
			<link rel="alternate" type="application/atom+xml" href="/blog/feed.atom">
			</head>Body
			`,
			wantFeed: true,
		},
		{
			body: `<!doctype html><head><meta charset="utf-8"><title>Fallback title</title>
			<link rel="alternate" type="application/atom+xml" href="/blog/feed.atom">
			<link rel="canonical" href="https://example.org/">
			</head>Body
			`,
			wantFeed: true,
		},
	}
	for i, c := range testCases {
		got := atomFeed([]byte(c.body))
		if (got != nil) != c.wantFeed {
			t.Fatalf("case %d: feed got %v, but want %v; body:\n%s",
				i, (got != nil), c.wantFeed, c.body)
		}
	}
}
