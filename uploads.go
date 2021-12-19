package main

import (
	"bytes"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

func (h *handler) uploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	var notePath string
	if docURL, err := url.Parse(r.FormValue("document")); err != nil ||
		docURL.Path == "" || docURL.Path == "/" || docURL.Path == "." || !fs.ValidPath(docURL.Path[1:]) {
		http.Error(w, "Invalid 'document' form field", http.StatusBadRequest)
		return
	} else {
		notePath = docURL.Path[1:]
	}
	const sizeLimit = 10 << 20
	r.Body = http.MaxBytesReader(w, r.Body, sizeLimit)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		log.Printf("FormFile: %v", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	defer f.Close()
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if hdr.Size <= 0 || hdr.Size > sizeLimit {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	filename, err := validateFilename(hdr.Filename)
	if err != nil {
		http.Error(w, "Bad file name", http.StatusBadRequest)
		return
	}
	buf := make([]byte, hdr.Size)
	if _, err := io.ReadFull(f, buf); err != nil {
		log.Printf("file upload ReadFull: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	sum := sha1.Sum(buf)
	fPath := path.Join(".files", base64.RawURLEncoding.EncodeToString(sum[:]), filename)
	_, err = h.stUploadFile.ExecContext(r.Context(),
		sql.Named("path", fPath),
		sql.Named("bytes", buf),
		sql.Named("notepath", notePath),
	)
	if err != nil {
		log.Printf("storing file: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	u := &url.URL{Path: "/" + fPath}
	json.NewEncoder(w).Encode(struct{ URL string }{URL: u.String()})
}

func validateFilename(name string) (string, error) {
	name = path.Base(name)
	if name == "" || name == "." || strings.Contains(name, "/") || !fs.ValidPath(name) {
		return "", errors.New("invalid file name")
	}
	return name, nil
}

// uploadFileInfo is a fs.FileInfo implementation for file uploads read access
type uploadFileInfo struct {
	name        string
	size, ctime int64
}

func (fi *uploadFileInfo) Name() string       { return fi.name }
func (fi *uploadFileInfo) Size() int64        { return fi.size }
func (fi *uploadFileInfo) Mode() fs.FileMode  { return 0444 }
func (fi *uploadFileInfo) ModTime() time.Time { return time.Unix(fi.ctime, 0) }
func (fi *uploadFileInfo) IsDir() bool        { return false }
func (fi *uploadFileInfo) Sys() any           { return nil }

// uploadFile is a fs.File implementation for file uploads read access
type uploadFile struct {
	r  *bytes.Reader
	fi uploadFileInfo
}

func (f *uploadFile) Stat() (fs.FileInfo, error) { return &f.fi, nil }

func (f *uploadFile) Read(b []byte) (int, error) {
	if f.r == nil {
		return 0, fs.ErrClosed
	}
	return f.r.Read(b)
}

func (f *uploadFile) Close() error {
	if f.r == nil {
		return fs.ErrClosed
	}
	f.r = nil
	return nil
}

type uploadsFS struct {
	stmt *sql.Stmt
}

func newUploadsFS(db *sql.DB) uploadsFS {
	return uploadsFS{stmt: mustPrepare(db, `SELECT Ctime, Bytes FROM files WHERE Path=@path`)}
}

func (fsys uploadsFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{
			Op:   "open",
			Path: name,
			Err:  fs.ErrInvalid,
		}
	}
	var cTime int64
	var body []byte
	if err := fsys.stmt.QueryRow(sql.Named("path", name)).Scan(&cTime, &body); err != nil {
		if err == sql.ErrNoRows {
			err = fs.ErrNotExist
		}
		return nil, &fs.PathError{
			Op:   "open",
			Path: name,
			Err:  err,
		}
	}
	return &uploadFile{
		r: bytes.NewReader(body),
		fi: uploadFileInfo{
			name:  path.Base(name),
			size:  int64(len(body)),
			ctime: cTime,
		},
	}, nil
}
