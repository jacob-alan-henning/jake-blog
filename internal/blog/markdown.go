package blog

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/russross/blackfriday/v2"
	bf "github.com/russross/blackfriday/v2"
)

type jakeRenderer struct {
	cacheUrl string
	*bf.HTMLRenderer
}

func (r *jakeRenderer) RenderNode(w io.Writer, node *bf.Node, entering bool) bf.WalkStatus {
	if node.Type == blackfriday.Image && entering {
		originalDest := string(node.Destination)
		if !strings.HasPrefix(originalDest, "https://") {
			fn := strings.Split(originalDest, "/")
			node.Destination = []byte(r.cacheUrl + fn[1])
		}
	}
	return r.HTMLRenderer.RenderNode(w, node, entering)
}

func markdownToHtml(markdownPath string, imageCache bool) (string, error) {
	escapedPath := filepath.Clean(markdownPath)
	mdFile, err := os.Open(escapedPath)
	if err != nil {
		return "", err
	}
	defer mdFile.Close()

	fileInfo, err := mdFile.Stat()
	if err != nil {
		return "", err
	}

	size := fileInfo.Size()
	buffer := make([]byte, size)

	_, err = mdFile.Read(buffer)
	if err != nil {
		return "", err
	}

	htmlRenderer := blackfriday.NewHTMLRenderer(blackfriday.HTMLRendererParameters{})
	cRenderer := &jakeRenderer{
		HTMLRenderer: htmlRenderer,
		cacheUrl:     "https://jakeblog-blog-image-cache.s3.us-east-1.amazonaws.com/",
	}

	var html []byte
	if imageCache {
		html = bf.Run(buffer, bf.WithRenderer(cRenderer))
	} else {
		html = bf.Run(buffer)
	}
	return string(html), nil
}
