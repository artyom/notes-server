// Program notes-export exports notes with a given tag as a bunch of HTML pages
// suitable to be served as a basic standalone website.
package main

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/artyom/notes-server/internal/markdown"
	gtext "github.com/yuin/goldmark/text"
	_ "modernc.org/sqlite"
)

func main() {
	log.SetFlags(0)
	args := runArgs{Tag: "public"}
	flag.StringVar(&args.DB, "db", args.DB, "`path` to notes database file")
	flag.StringVar(&args.Dir, "dir", args.Dir, "destination `directory` to export notes into")
	flag.StringVar(&args.Tag, "tag", args.Tag, "export notes which have this `tag` assigned")
	flag.StringVar(&args.IndexTemplate, "index", args.IndexTemplate,
		"`path` to the auto-generated index page template file; no value enables built-in template")
	flag.StringVar(&args.PageTemplate, "page", args.PageTemplate,
		"`path` to the page template file; no value enables built-in template")
	flag.Parse()
	if err := run(args); err != nil {
		log.Fatal(err)
	}
}

type runArgs struct {
	Tag           string
	DB            string
	Dir           string
	IndexTemplate string
	PageTemplate  string
}

func (a *runArgs) validate() error {
	if a.Tag == "" {
		return errors.New("tag must be set")
	}
	if a.DB == "" {
		return errors.New("database must be set")
	}
	if a.Dir == "" {
		return errors.New("destination directory must be set")
	}
	return nil
}

func run(args runArgs) error {
	if err := args.validate(); err != nil {
		return err
	}
	var err error
	indexTemplate := indexTemplate
	pageTemplate := pageTemplate
	if args.IndexTemplate != "" {
		if indexTemplate, err = template.ParseFiles(args.IndexTemplate); err != nil {
			return err
		}
	}
	if args.PageTemplate != "" {
		if pageTemplate, err = template.ParseFiles(args.PageTemplate); err != nil {
			return err
		}
	}
	if _, err := os.Stat(args.DB); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", args.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := savePages(tx, args, pageTemplate, indexTemplate); err != nil {
		return err
	}
	return saveAttachments(tx, args)
}

func saveAttachments(tx *sql.Tx, args runArgs) error {
	rows, err := tx.Query(`WITH exp AS (
		SELECT DISTINCT notes.Path FROM notes, json_each(notes.Tags)
		WHERE json_each.value=?
	)
	SELECT files.Path,Bytes,Ctime FROM files, exp
	WHERE files.NotePath=exp.Path`, args.Tag)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var att struct {
			path  string
			ctime int64
			data  []byte
		}
		if err := rows.Scan(&att.path, &att.data, &att.ctime); err != nil {
			return err
		}
		if !fs.ValidPath(att.path) {
			return fmt.Errorf("%q is not a valid path", att.path)
		}
		dst := filepath.Join(args.Dir, filepath.FromSlash(att.path))
		if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
			return err
		}
		if err := os.WriteFile(dst, att.data, 0666); err != nil {
			return err
		}
		mtime := time.Unix(att.ctime, 0).UTC()
		if err := os.Chtimes(dst, mtime, mtime); err != nil {
			return err
		}
		log.Print(dst)
	}
	return rows.Err()
}

func savePages(tx *sql.Tx, args runArgs, pageTemplate, indexTemplate *template.Template) error {
	rows, err := tx.Query(`SELECT DISTINCT notes.Path,Title,Text,Mtime,Tags FROM notes, json_each(Tags)
	WHERE json_each.value=? ORDER BY Mtime DESC`, args.Tag)
	if err != nil {
		return err
	}
	defer rows.Close()
	buf := new(bytes.Buffer)
	type indexRecord struct {
		Path, Title string
		Snippet     string // first paragraph text
		Mtime       time.Time
	}
	var index []indexRecord
	var bodyBytes []byte
	for rows.Next() {
		bodyBytes = bodyBytes[:0]
		var note struct {
			Title   string // from db
			path    string // from db
			mtime   int64  // from db
			Body    template.HTML
			Mtime   time.Time
			TOC     []markdown.HeadingInfo
			HasCode bool
			Tags    []string
		}
		var tagsBytes []byte
		if err := rows.Scan(&note.path, &note.Title, &bodyBytes, &note.mtime, &tagsBytes); err != nil {
			return err
		}
		if !fs.ValidPath(note.path) {
			return fmt.Errorf("%q is not a valid path", note.path)
		}
		if note.Tags, err = decodeTags(tagsBytes, args.Tag); err != nil {
			return fmt.Errorf("decoding note %q tags: %w", note.path, err)
		}
		note.Mtime = time.Unix(note.mtime, 0).UTC()
		doc := markdown.Markdown.Parser().Parse(gtext.NewReader(bodyBytes))
		if note.TOC, err = markdown.AssignHeaderIDs(bodyBytes, doc); err != nil {
			return fmt.Errorf("path: %s, title: %s, assigning header ids: %w", note.path, note.Title, err)
		}
		buf.Reset()
		if err := markdown.Markdown.Renderer().Render(buf, bodyBytes, doc); err != nil {
			return err
		}
		if len(note.TOC) < 2 || !markdown.WordCountAtLeast(bodyBytes, 300) {
			note.TOC = nil
		}
		note.HasCode = bytes.Contains(buf.Bytes(), []byte("<pre><code"))
		note.Body = template.HTML(buf.String())
		buf.Reset()
		if err := pageTemplate.Execute(buf, note); err != nil {
			return err
		}
		dst := filepath.Join(args.Dir, filepath.FromSlash(note.path))
		if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
			return err
		}
		if err := os.WriteFile(dst, buf.Bytes(), 0666); err != nil {
			return err
		}
		if err := os.Chtimes(dst, note.Mtime, note.Mtime); err != nil {
			return err
		}
		log.Printf("%s (%s)", dst, note.Title)
		idx := indexRecord{Title: note.Title, Path: note.path, Mtime: note.Mtime}
		snippet := markdown.FirstParagraphText(bodyBytes, doc)
		if r, _ := utf8.DecodeLastRuneInString(snippet); unicode.Is(unicode.Sentence_Terminal, r) {
			idx.Snippet = snippet
		}
		index = append(index, idx)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(index) == 0 {
		return errors.New("no matching pages")
	}
	buf.Reset()
	if err := indexTemplate.Execute(buf, index); err != nil {
		return err
	}
	dst := filepath.Join(args.Dir, "index.html")
	if err := os.WriteFile(dst, buf.Bytes(), 0666); err != nil {
		return err
	}
	log.Print(dst)
	return nil
}

func decodeTags(tagsBytes []byte, tagSkip string) ([]string, error) {
	var allTags []string
	if err := json.Unmarshal(tagsBytes, &allTags); err != nil {
		return nil, err
	}
	out := allTags[:0]
	for _, tag := range allTags {
		if tag != tagSkip {
			out = append(out, tag)
		}
	}
	return out, nil
}

//go:embed index.html
var defaultIndexTemplate string

//go:embed page.html
var defaultPageTemplate string

var indexTemplate = template.Must(template.New("").Parse(defaultIndexTemplate))
var pageTemplate = template.Must(template.New("").Parse(defaultPageTemplate))
