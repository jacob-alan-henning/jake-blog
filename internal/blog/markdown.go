package blog

import (
	"os"
	"path/filepath"

	"github.com/russross/blackfriday/v2"
)

func markdownToHtml(markdownPath string) (string, error) {
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

	html := blackfriday.Run(buffer)
	return string(html), nil
}
