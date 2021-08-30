// TODO describe program
package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/artyom/httpgzip"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"modernc.org/sqlite"
)

func main() {
	log.SetFlags(0)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	args := runArgs{
		addr:     "localhost:8080",
		database: "notes.sqlite",
	}
	flag.StringVar(&args.addr, "addr", args.addr, "address to listen")
	flag.StringVar(&args.database, "db", args.database, "`path` to the database")
	flag.Parse()
	if err := run(ctx, args); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

type runArgs struct {
	addr, database string
}

func run(ctx context.Context, args runArgs) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	db, err := sql.Open("sqlite", args.database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := initSchema(ctx, db); err != nil {
		return err
	}
	h := newHandler(db)
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.Handle("/.files/", http.FileServer(http.FS(newUploadsFS(db))))
	mux.HandleFunc("/.files", http.HandlerFunc(h.uploadFile))
	mux.HandleFunc("/robots.txt", http.HandlerFunc(noRobots))
	mux.HandleFunc("/favicon.ico", http.HandlerFunc(favicon))
	afs, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("/.assets/", http.StripPrefix("/.assets/", http.FileServer(http.FS(afs))))
	srv := &http.Server{
		Addr:    args.addr,
		Handler: httpgzip.New(mux),
	}
	log.Printf("serving at http://%s/", srv.Addr)
	go func() { <-ctx.Done(); srv.Shutdown(ctx) }()
	defer db.Exec(`PRAGMA optimize`)
	return srv.ListenAndServe()
}

type handler struct {
	stSearchNotes *sql.Stmt
	stNotesIndex  *sql.Stmt
	stEditPage    *sql.Stmt
	stRenderPage  *sql.Stmt
	stDeletePage  *sql.Stmt
	stSavePage    *sql.Stmt
	stUploadFile  *sql.Stmt
}

func newHandler(db *sql.DB) *handler {
	return &handler{
		stSearchNotes: mustPrepare(db, `SELECT Title, Path, Tags, snippet(notes_fts, 2, '<ftsMark>', '</ftsMark>', '...', 20)
			FROM notes_fts WHERE notes_fts MATCH ? ORDER BY rank;`),
		stNotesIndex: mustPrepare(db, `SELECT Title, Path, Mtime, Tags FROM notes ORDER BY Mtime DESC`),
		stEditPage:   mustPrepare(db, `SELECT Text FROM notes WHERE Path=@path`),
		stRenderPage: mustPrepare(db, `SELECT Title, Text, Mtime, Tags FROM notes WHERE Path=@path`),
		stDeletePage: mustPrepare(db, `DELETE FROM notes WHERE Path=@path`),
		stSavePage: mustPrepare(db, `INSERT INTO notes(Path,Title,Text,Tags)
			VALUES(@path,@title,@text,@tags)
			ON CONFLICT(Path) DO UPDATE
			SET Title=excluded.Title, Text=excluded.Text, Mtime=excluded.Mtime, Tags=excluded.Tags`),
		stUploadFile: mustPrepare(db, `INSERT OR IGNORE INTO files(Path,Bytes,NotePath) VALUES(@path,@bytes,@notepath)`),
	}
}

func mustPrepare(db *sql.DB, statement string) *sql.Stmt {
	st, err := db.Prepare(statement)
	if err != nil {
		panic(err)
	}
	return st
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Frame-Options", "DENY")
	// log.Printf("%s %s", r.Method, r.URL)
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut:
	default:
		w.Header().Set("Allow", "GET, POST, PUT")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		h.renderIndex(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Query().Has("edit") {
		h.editPage(w, r)
		return
	}
	if r.Method == http.MethodPost {
		h.savePage(w, r)
		return
	}
	if r.Method == http.MethodGet {
		h.renderPage(w, r)
		return
	}
}

func (h *handler) renderIndex(w http.ResponseWriter, r *http.Request) {
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		entries, err := searchNotes(r.Context(), h.stSearchNotes, q)
		if err != nil && err != sql.ErrNoRows {
			var se *sqlite.Error
			if errors.As(err, &se) && se.Code() == 1 {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			log.Printf("search for %q: %v", q, err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		searchResultsTemplate.Execute(w, struct {
			Term    string
			Results []indexEntry
		}{Term: q, Results: entries})
		return
	}
	entries, err := notesIndex(r.Context(), h.stNotesIndex)
	if err != nil {
		log.Printf("index: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	indexTemplate.Execute(w, entries)
}

func searchNotes(ctx context.Context, stmt *sql.Stmt, term string) ([]indexEntry, error) {
	if term == "" {
		return nil, errors.New("empty search term")
	}
	rows, err := stmt.QueryContext(ctx, term)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []indexEntry
	var tagsJson []byte
	for rows.Next() {
		var ent indexEntry
		var snippet string
		tagsJson = tagsJson[:0]
		if err := rows.Scan(&ent.Title, &ent.Path, &tagsJson, &snippet); err != nil {
			return nil, err
		}
		if len(tagsJson) != 0 {
			_ = json.Unmarshal(tagsJson, &ent.Tags)
		}
		ent.Snippet = template.HTML(htmlEscaper.Replace(snippet))
		out = append(out, ent)
	}
	return out, rows.Err()
}

func notesIndex(ctx context.Context, stmt *sql.Stmt) ([]indexEntry, error) {
	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []indexEntry
	var tagsJson []byte
	for rows.Next() {
		var mtimeUnix int64
		var ent indexEntry
		tagsJson = tagsJson[:0]
		if err := rows.Scan(&ent.Title, &ent.Path, &mtimeUnix, &tagsJson); err != nil {
			return nil, err
		}
		ent.Mtime = time.Unix(mtimeUnix, 0)
		if len(tagsJson) != 0 {
			if err := json.Unmarshal(tagsJson, &ent.Tags); err != nil {
				// TODO: maybe just ignore?
				return nil, fmt.Errorf("unmarshaling tags for %q: %w", ent.Path, err)
			}
		}
		out = append(out, ent)
	}
	return out, rows.Err()
}

type indexEntry struct {
	Title, Path string
	Snippet     template.HTML
	Mtime       time.Time
	Tags        []string
}

func (h *handler) editPage(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimLeft(r.URL.Path, "/")
	if p == "." || !fs.ValidPath(p) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	var text string
	switch err := h.stEditPage.QueryRowContext(r.Context(), sql.Named("path", p)).Scan(&text); err {
	case nil, sql.ErrNoRows:
	default:
		log.Printf("edit %q: %v", r.URL, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if text == "" {
		text = "# Page title\n\nPut your text here, save with Cmd-s.\n"
	}
	if r.URL.RawQuery == "edit=basic" {
		editPageTemplate.Execute(w, struct{ Text string }{Text: text})
		return
	}
	richEditPageTemplate.Execute(w, struct{ Text string }{Text: text})
}

func (h *handler) renderPage(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimLeft(r.URL.Path, "/")
	if p == "." || !fs.ValidPath(p) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	var text, title string
	var mtime int64
	var tagsJson []byte
	switch err := h.stRenderPage.QueryRowContext(r.Context(), sql.Named("path", p)).Scan(&title, &text, &mtime, &tagsJson); err {
	case nil:
	case sql.ErrNoRows:
		pageNotFound(w, r)
		return
	default:
		log.Printf("get %q: %v", r.URL, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	var tags []string
	if len(tagsJson) != 0 {
		if err := json.Unmarshal(tagsJson, &tags); err != nil {
			log.Printf("unmarshaling %q tags %q: %v", r.URL, tagsJson, err)
		}
	}
	buf := new(bytes.Buffer)
	if err := markdown.Convert([]byte(text), buf); err != nil {
		log.Printf("render %q: %v", r.URL, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Last-Modified", time.Unix(mtime, 0).UTC().Format(http.TimeFormat))
	pageTemplate.Execute(w, struct {
		Title   string
		Text    template.HTML
		HasCode bool
		Tags    []string
	}{
		Title:   title,
		Text:    template.HTML(buf.String()),
		HasCode: bytes.Contains(buf.Bytes(), []byte("<pre><code")),
		Tags:    tags,
	})
}

func (h *handler) savePage(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimLeft(r.URL.Path, "/")
	if p == "." || !fs.ValidPath(p) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("delete") == "true" {
		_, err := h.stDeletePage.ExecContext(r.Context(), sql.Named("path", p))
		switch err {
		case nil, sql.ErrNoRows:
			http.Redirect(w, r, "/", http.StatusSeeOther)
		default:
			log.Printf("deleting %q: %v", p, err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}
	text := strings.TrimSpace(r.PostForm.Get("text"))
	if text == "" {
		http.Error(w, "Empty text", http.StatusBadRequest)
		return
	}
	if !utf8.ValidString(text) {
		http.Error(w, "Text is not a valid utf8", http.StatusBadRequest)
		return
	}
	text = crlf.Replace(text)
	tags := noteTags(text)
	var tagsJson []byte
	if len(tags) != 0 {
		var err error
		if tagsJson, err = json.Marshal(tags); err != nil {
			panic(err)
		}
	}
	_, err := h.stSavePage.ExecContext(r.Context(),
		sql.Named("path", p),
		sql.Named("title", textTitle(text)),
		sql.Named("text", text),
		sql.Named("tags", tagsJson),
	)
	if err != nil {
		log.Printf("updating %q: %v", p, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
}

func initSchema(ctx context.Context, db *sql.DB) error {
	for _, s := range [...]string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=normal`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS notes(
			Path TEXT PRIMARY KEY NOT NULL,
			Title TEXT NOT NULL,
			Text TEXT NOT NULL,
			Ctime INT NOT NULL DEFAULT (strftime('%s','now')), -- unix timestamp of time created
			Mtime INT NOT NULL DEFAULT (strftime('%s','now')), -- unix timestamp of time updated
			Tags TEXT check(Tags is NULL OR(json_valid(Tags) AND json_type(Tags)='array'))
		)`,
		`CREATE INDEX IF NOT EXISTS notesMtime ON notes(Mtime DESC)`,
		// full text search-related
		`CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(Path, Title, 'Text', Tags, content=notes)`,
		`CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
			INSERT INTO notes_fts(rowid, Path, Title, "Text", Tags)
				VALUES (new.rowid, new.Path, new.Title, new.Text, new.Tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
			INSERT INTO notes_fts(notes_fts, rowid, Path, Title, "Text", Tags)
				VALUES ('delete', old.rowid, old.Path, old.Title, old.Text, old.Tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE ON notes BEGIN
			INSERT INTO notes_fts(notes_fts, rowid, Path, Title, "Text", Tags)
				VALUES ('delete', old.rowid, old.Path, old.Title, old.Text, old.Tags);
			INSERT INTO notes_fts(rowid, Path, Title, "Text", Tags)
				VALUES (new.rowid, new.Path, new.Title, new.Text, new.Tags);
		END`,
		// file uploads (temporary, to be later offloaded to S3)
		`CREATE TABLE IF NOT EXISTS files(
			Path TEXT PRIMARY KEY NOT NULL,
			Bytes BLOB NOT NULL,
			Ctime INT NOT NULL DEFAULT (strftime('%s','now')), -- unix timestamp of time created
			NotePath TEXT NOT NULL REFERENCES notes(Path) ON DELETE CASCADE
		)`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("SQL statement %q: %w", s, err)
		}
	}
	return nil
}

func textTitle(text string) string {
	const cutset = "#\t\r\n "
	var out string
	i := strings.IndexByte(text, '\n')
	if i == -1 {
		out = strings.Trim(text, cutset)
	} else {
		out = strings.Trim(text[:i], cutset)
	}
	if out == "" {
		out = "Untitled"
	}
	return out
}

func noRobots(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, "User-agent: *\nDisallow: /\n")
}

func favicon(w http.ResponseWriter, _ *http.Request) {
	f, err := assetsFS.Open("assets/favicon-32x32.png")
	if err != nil {
		log.Printf("favicon open: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	io.Copy(w, f)
}

func pageNotFound(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
	page404Template.Execute(w, r.URL.Path)
}

func noteTags(text string) []string {
	const prefix = `<!--`
	const suffix = `-->`
	i := strings.Index(text, prefix)
	if i == -1 {
		return nil
	}
	start := i + len(prefix)
	end := strings.Index(text, suffix)
	if end == -1 || end < start {
		return nil
	}
	snippet := text[start:end]
	const tagsWord = "Tags:"
	if i = strings.Index(snippet, tagsWord); i == -1 {
		return nil
	}
	snippet = snippet[i+len(tagsWord):]
	if i = strings.Index(snippet, "\n"); i != -1 {
		snippet = snippet[:i]
	}
	ss := strings.Split(snippet, ",")
	if len(ss) == 0 {
		return nil
	}
	out := ss[:0]
	seen := make(map[string]struct{})
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		out = append(out, s)
		seen[s] = struct{}{}
	}
	return out[:len(out):len(out)]
}

var (
	//go:embed assets
	assetsFS embed.FS

	//go:embed templates
	templateFS embed.FS

	pageTemplate          = template.Must(template.ParseFS(templateFS, "templates/page.html"))
	editPageTemplate      = template.Must(template.ParseFS(templateFS, "templates/editPage.html"))
	richEditPageTemplate  = template.Must(template.ParseFS(templateFS, "templates/monaco.html"))
	indexTemplate         = template.Must(template.ParseFS(templateFS, "templates/index.html"))
	searchResultsTemplate = template.Must(template.ParseFS(templateFS, "templates/search-results.html"))
	page404Template       = template.Must(template.ParseFS(templateFS, "templates/404.html"))
)

var markdown = goldmark.New(
	goldmark.WithRendererOptions(html.WithUnsafe()),
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

var crlf = strings.NewReplacer("\r\n", "\n")

// htmlEscaper is a copy of a non-exported html.htmlEscaper backing
// html.EscapeString with the addition of two exceptions for the <mark> element
// tags coming from sqlite FTS snippet() function.
var htmlEscaper = strings.NewReplacer(
	`<ftsMark>`, `<mark>`,
	`</ftsMark>`, `</mark>`,
	`&`, "&amp;",
	`'`, "&#39;", // "&#39;" is shorter than "&apos;" and apos was not in HTML until HTML5.
	`<`, "&lt;",
	`>`, "&gt;",
	`"`, "&#34;", // "&#34;" is shorter than "&quot;".
)
