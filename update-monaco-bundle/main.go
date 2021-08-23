// TODO describe program
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	flag.Parse()
	if err := run(ctx, flag.Arg(0)); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, monacoURL string) error {
	if monacoURL == "" {
		return fmt.Errorf("usage: %s https://.../monaco-editor-0.27.0.tgz", filepath.Base(os.Args[0]))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, monacoURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %q", resp.Status)
	}
	rd, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer rd.Close()

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})
	defer zw.Close()

	var hasLicense bool
	var cnt int
	tr := tar.NewReader(rd)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		const prefix = "package/min/"
		if !(strings.HasPrefix(hdr.Name, prefix) || strings.HasSuffix(hdr.Name, "/LICENSE")) || !fs.ValidPath(hdr.Name) {
			continue
		}
		if ok, _ := path.Match(prefix+"vs/editor/editor.main.nls.*.js", hdr.Name); ok {
			continue
		}
		fi := hdr.FileInfo()
		if !fi.Mode().IsRegular() {
			continue
		}
		zipHeader, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		if zipHeader.Name != "LICENSE" {
			zipHeader.Name = strings.TrimPrefix(hdr.Name, prefix)
		} else {
			hasLicense = true
		}
		zipHeader.Method = zip.Deflate
		w, err := zw.CreateHeader(zipHeader)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, tr); err != nil {
			return err
		}
		cnt++
	}
	if !hasLicense {
		return errors.New("cannot find a LICENSE file in monaco distribution")
	}
	if cnt < 50 {
		return errors.New("suspiciously few files matched in monaco distribution")
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile("monaco-minimal.zip", buf.Bytes(), 0666)
}
