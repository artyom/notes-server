package main

import (
	"testing"
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
