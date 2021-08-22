// TODO describe program
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	_ "modernc.org/sqlite"
)

func main() {
	log.SetFlags(0)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	db, err := sql.Open("sqlite", "notes.sqlite")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := initSchema(ctx, db); err != nil {
		return err
	}
	h := &handler{db: db}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("/favicon.ico", http.HandlerFunc(favicon))
	afs, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("/.assets/", http.StripPrefix("/.assets/", http.FileServer(http.FS(afs))))
	mux.Handle("/.assets/monaco/", http.StripPrefix("/.assets/monaco/", http.FileServer(http.FS(monacoBundleFS))))
	srv := &http.Server{
		Addr:    "localhost:8080",
		Handler: mux,
	}
	log.Printf("serving at http://%s/", srv.Addr)
	go func() { <-ctx.Done(); srv.Shutdown(ctx) }()
	return srv.ListenAndServe()
}

type handler struct {
	db   *sql.DB
	once sync.Once
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// log.Printf("%s %s", r.Method, r.URL)
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut:
	default:
		w.Header().Set("Allow", "GET, POST, PUT")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
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
	entries, err := notesIndex(r.Context(), h.db)
	if err != nil {
		log.Printf("index: %v", err)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	indexTemplate.Execute(w, entries)
}

func notesIndex(ctx context.Context, db *sql.DB) ([]indexEntry, error) {
	const query = `SELECT Title, Path, Mtime FROM notes ORDER BY Mtime DESC`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []indexEntry
	for rows.Next() {
		var mtimeUnix int64
		var ent indexEntry
		if err := rows.Scan(&ent.Title, &ent.Path, &mtimeUnix); err != nil {
			return nil, err
		}
		ent.Mtime = time.Unix(mtimeUnix, 0)
		out = append(out, ent)
	}
	return out, rows.Err()
}

type indexEntry struct {
	Title, Path string
	Mtime       time.Time
}

func (h *handler) editPage(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimLeft(r.URL.Path, "/")
	if p == "." || !fs.ValidPath(p) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	var text string
	const query = `SELECT Text FROM notes WHERE Path=@path`
	switch err := h.db.QueryRowContext(r.Context(), query, sql.Named("path", p)).Scan(&text); err {
	case nil, sql.ErrNoRows:
	default:
		log.Printf("edit %q: %v", r.URL, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// editPageTemplate.Execute(w, struct{ Text string }{Text: text})
	richEditPageTemplate.Execute(w, struct{ Text string }{Text: text})
}

func (h *handler) renderPage(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimLeft(r.URL.Path, "/")
	if p == "." || !fs.ValidPath(p) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	var text, title string
	const query = `SELECT Title, Text FROM notes WHERE Path=@path`
	switch err := h.db.QueryRowContext(r.Context(), query, sql.Named("path", p)).Scan(&title, &text); err {
	case nil:
	case sql.ErrNoRows:
		http.NotFound(w, r)
		return
	default:
		log.Printf("get %q: %v", r.URL, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	buf := new(bytes.Buffer)
	if err := markdown.Convert([]byte(text), buf); err != nil {
		log.Printf("render %q: %v", r.URL, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	pageTemplate.Execute(w, struct {
		Title string
		Text  template.HTML
	}{Title: title, Text: template.HTML(buf.String())})
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
		const query = `DELETE FROM notes WHERE Path=@path`
		_, err := h.db.ExecContext(r.Context(), query, sql.Named("path", p))
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
	const query = `INSERT INTO notes(Path,Title,Text) VALUES(@path,@title,@text)
	ON CONFLICT(Path) DO UPDATE SET Title=excluded.Title, Text=excluded.Text, Mtime=excluded.Mtime`
	_, err := h.db.ExecContext(r.Context(), query,
		sql.Named("path", p),
		sql.Named("title", textTitle(text)),
		sql.Named("text", text),
	)
	if err != nil {
		log.Printf("updating %q: %v", p, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
}

func (h *handler) init() {
	// TODO
}

func initSchema(ctx context.Context, db *sql.DB) error {
	for _, s := range [...]string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=normal`,
		`CREATE TABLE IF NOT EXISTS notes(
			Path TEXT PRIMARY KEY NOT NULL,
			Title TEXT NOT NULL,
			Text TEXT NOT NULL,
			Ctime INT NOT NULL DEFAULT (strftime('%s','now')), -- unix timestamp of time created
			Mtime INT NOT NULL DEFAULT (strftime('%s','now'))  -- unix timestamp of time updated
		)`,
		`CREATE INDEX IF NOT EXISTS notesMtime ON notes(Mtime DESC)`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("SQL statement %q: %w", s, err)
		}
	}
	return nil
}

func textTitle(text string) string {
	const cutset = "#\t\r\n "
	i := strings.IndexByte(text, '\n')
	if i == -1 {
		return strings.Trim(text, cutset)
	}
	return strings.Trim(text[:i], cutset)
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

func init() {
	var err error
	monacoBundleFS, err = zip.NewReader(bytes.NewReader(monacoBundle), int64(len(monacoBundle)))
	if err != nil {
		panic(err)
	}
}

var (
	//go:embed assets
	assetsFS embed.FS

	//go:embed monaco-minimal.zip
	monacoBundle   []byte
	monacoBundleFS *zip.Reader

	//go:embed editPage.html
	editPageBody string
	//go:embed page.html
	pageBody string
	//go:embed index.html
	indexBody string
	//go:embed monaco.html
	richEditPageBody string

	pageTemplate         = template.Must(template.New("page").Parse(pageBody))
	editPageTemplate     = template.Must(template.New("edit").Parse(editPageBody))
	richEditPageTemplate = template.Must(template.New("edit").Parse(richEditPageBody))
	indexTemplate        = template.Must(template.New("index").Parse(indexBody))
)

var markdown = goldmark.New(goldmark.WithExtensions(extension.Table))

var crlf = strings.NewReplacer("\r\n", "\n")
