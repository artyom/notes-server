package main

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	ver := "11.5.1"
	flag.StringVar(&ver, "version", ver, "highlight.js version to download")
	flag.Parse()
	if err := run(ver); err != nil {
		log.Fatal(err)
	}
}

func run(ver string) error {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})
	const base = "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/"
	for _, f := range [...]struct {
		src, dst, ct string
	}{
		{ver + "/highlight.min.js", ver + "/highlight.min.js", "application/javascript"},
		{ver + "/styles/foundation.min.css", ver + "/foundation.min.css", "text/css"},
		{ver + "/styles/base16/ashes.min.css", ver + "/ashes.min.css", "text/css"},
	} {
		url := base + f.src
		data, ct, err := download(url)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(ct, f.ct) {
			return fmt.Errorf("%s content-type is %q, want %q", url, ct, f.ct)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.dst, Method: zip.Deflate})
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile("hljs-bundle.zip", buf.Bytes(), 0666)
}

func download(url string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("bad status: %s", resp.Status)
	}
	if resp.ContentLength > 1<<20 {
		return nil, "", fmt.Errorf("content lenght is suspiciously large: %d", resp.ContentLength)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}
