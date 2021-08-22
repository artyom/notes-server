// TODO describe program
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path"
	"strings"
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
	// https://registry.npmjs.org/monaco-editor/-/monaco-editor-0.27.0.tgz
	// dir, err := os.MkdirTemp("", "monaco-editor-update-*")
	// if err != nil {
	// 	return err
	// }
	// defer os.RemoveAll(dir)

	tgz, err := os.Open(os.ExpandEnv("${HOME}/Downloads/monaco.tar.gz"))
	if err != nil {
		return err
	}
	defer tgz.Close()
	rd, err := gzip.NewReader(tgz)
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
		if !strings.HasPrefix(hdr.Name, prefix) || !fs.ValidPath(hdr.Name) {
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
		zipHeader.Name = strings.TrimPrefix(hdr.Name, prefix)
		zipHeader.Method = zip.Deflate
		w, err := zw.CreateHeader(zipHeader)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, tr); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile("monaco-minimal.zip", buf.Bytes(), 0666)
}
