package blog

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Article struct {
	Title    string
	FileName string
	Content  string
	Url      string
	Date     time.Time
}

type BlogManager struct {
	Articles     map[string]Article
	HtmlList     string // html snippet - list of articles
	SiteMap      string
	Config       *Config
	articleMutex sync.RWMutex
	updateChan   chan struct{} // Single channel for all updates
}

func NewBlogManager(config *Config) *BlogManager {
	return &BlogManager{
		Articles:   make(map[string]Article),
		Config:     config,
		updateChan: make(chan struct{}, 1),
	}
}

func (bm *BlogManager) GetArticle(name string) (Article, bool) {
	bm.articleMutex.RLock()
	defer bm.articleMutex.RUnlock()
	article, exists := bm.Articles[name]
	return article, exists
}

// start update handler and handle signals which force update
func (bm *BlogManager) ListenForUpdates(ctx context.Context) {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	// handle blogmanager signals
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighup:
				log.Printf("SIGHUP recieived updating content")
				bm.TriggerUpdate()
			case <-bm.updateChan:
				if err := bm.updateContent(); err != nil {
					log.Printf("Update failed: %v", err)
				}
			}
		}
	}()
}

func (bm *BlogManager) TriggerUpdate() {
	select {
	case bm.updateChan <- struct{}{}:
		log.Printf("Update triggered")
	default:
		log.Printf("Update already pending, skipping...")
	}
}

func (bm *BlogManager) extractTitle(filepath string) string {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return strings.TrimSuffix(path.Base(filepath), ".md")
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}

	return strings.TrimSuffix(path.Base(filepath), ".md")
}

func (bm *BlogManager) updateContent() error {
	err := FetchMarkdownRepo(bm.Config)
	if err != nil {
		return fmt.Errorf("error cloning md repository: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(bm.Config.ContentDir, "*.md"))
	if err != nil {
		return fmt.Errorf("could not find md files: %w", err)
	}

	newArticles := make(map[string]Article)
	var links []string

	artTmpl := `
        <!DOCTYPE html>
        <html>
        <head>
            <title>%s</title>
            <link rel="stylesheet" type="text/css" href="/static/styles.css">
            <link rel="icon" href="/favicon.ico" type="image/x-icon" />
        </head>
        <body>
            %s
        </body>
        </html>
    `

	for _, file := range files {
		fileName := strings.TrimSuffix(filepath.Base(file), ".md")
		headerTitle := bm.extractTitle(file)

		lastModified, err := getFileLastModified(bm.Config, filepath.Base(file))
		if err != nil {
			log.Printf("Error processing last modified date for %s: %v", fileName, err)
			continue
		}

		fileContent, err := markdownToHtml(file)
		if err != nil {
			log.Printf("Error processing %s: %v", fileName, err)
			continue
		}
		html := fmt.Sprintf(artTmpl, headerTitle, fileContent)

		newArticles[fileName] = Article{
			Title:    headerTitle,
			FileName: fileName,
			Content:  html,
			Url:      fmt.Sprintf("/article/%s", fileName),
			Date:     lastModified,
		}

		links = append(links, fmt.Sprintf(`<li><a href="/article/%s" target="_blank" rel="noopener noreferrer">%s</a> -- <span class="date">%s</span> </li>`,
			fileName, headerTitle, lastModified.Format("Jan 2, 2006")))
	}

	bm.articleMutex.Lock()
	bm.Articles = newArticles
	bm.HtmlList = strings.Join(links, "<br/>")
	bm.articleMutex.Unlock()

	log.Printf("Content updated successfully: loaded %d articles", len(newArticles))
	return nil
}
