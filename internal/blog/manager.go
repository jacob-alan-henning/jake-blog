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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Article struct {
	Title    string
	FileName string
	Content  []byte
	URL      string
	Date     time.Time
}

type BlogManager struct {
	Articles     map[string]Article
	HTMLList     []byte // html snippet - list of articles
	SiteMap      string
	RSSFeed      []byte
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

func (bm *BlogManager) GetRssFeed() []byte {
	bm.articleMutex.RLock()
	defer bm.articleMutex.RUnlock()
	return bm.RSSFeed
}

// start update handler and handle signals which force update
func (bm *BlogManager) listenForUpdates(ctx context.Context) {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	// handle blogmanager signals
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighup:
				managerLogger.Info().Msg("SIGHUP recieived")
				bm.TriggerUpdate()
			case <-bm.updateChan:
				if err := bm.updateContent(); err != nil {
					managerLogger.Error().Msgf("update failed: %v", err)
				}
			}
		}
	}()
}

func (bm *BlogManager) TriggerUpdate() {
	select {
	case bm.updateChan <- struct{}{}:
		managerLogger.Info().Msg("content update triggered")
	default:
		managerLogger.Info().Msg("content update triggered")
		managerLogger.Warn().Msg("skipping content update: content update already pending")
	}
}

func (bm *BlogManager) extractTitle(filepath string) string {
	content, err := os.ReadFile(filepath) // #nosec G304 -- file is from trusted source
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

func (bm *BlogManager) createArticleFromFileName(file string) (*Article, error) {
	artTmpl := `
        <!DOCTYPE html>
        <html>
        <head>
            <title>%s</title>
            <link rel="stylesheet" type="text/css" href="/article.css">
            <link rel="icon" href="/favicon.ico" type="image/x-icon" />
        </head>
        <body>
            <a href="/" class="home-link">Home</a>
            %s
        </body>
        </html>
    `

	fileName := strings.TrimSuffix(filepath.Base(file), ".md")
	headerTitle := bm.extractTitle(file)

	lastModified, err := getFileLastModified(bm.Config, filepath.Base(file))
	if err != nil {
		managerLogger.Error().Str("file", fileName).Msgf("failed to process article last modified date: %v", err)
		return nil, err
	}

	fileContent, err := markdownToHTML(file, bm.Config.IMAGECACHE)
	if err != nil {
		log.Printf("Error processing %s: %v", fileName, err)
		managerLogger.Error().Msgf("failed to convert markdown to html: %v", err)
		return nil, err
	}
	html := fmt.Sprintf(artTmpl, headerTitle, fileContent)

	return &Article{
		Title:    headerTitle,
		FileName: fileName,
		Content:  []byte(html),
		URL:      fmt.Sprintf("/article/%s", fileName),
		Date:     lastModified,
	}, nil
}

func creatRSSitemFromArticle(art *Article) string {
	return fmt.Sprintf(`
		<item>
		<title> %s </title>
		<link> %s </link>
		<pubDate> %s </pubDate>
		<guid isPermaLink="true"> %s </guid>
		</item>
		`,
		art.Title,
		fmt.Sprintf("https://jake-henning.com%s", art.URL),
		art.Date.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		fmt.Sprintf("https://jake-henning.com%s", art.URL),
	)
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
	var rssBuilder strings.Builder

	rssBuilder.WriteString(`
     <rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
      <channel>
        <title> Jacob Henning's Blog </title>
        <link> https://jake-henning.com </link>
        <description> The personal blog of Jacob Henning </description>
        <atom:link href="https://jake-henning.com/feed" rel="self" type="application/rss+xml" />
   `)

	for _, file := range files {
		fArt, err := bm.createArticleFromFileName(file)
		if err != nil {
			managerLogger.Warn().Msgf("failed to load article %s: %v", file, err)
			continue
		}
		newArticles[fArt.FileName] = *fArt

		rssBuilder.WriteString(creatRSSitemFromArticle(fArt))
	}

	keys := make([]string, 0, len(newArticles))
	for k := range newArticles {
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool {
		return newArticles[keys[i]].Date.After(newArticles[keys[j]].Date)
	})

	for _, key := range keys {
		arti := newArticles[key]

		// html list
		links = append(links, fmt.Sprintf(`<li><a href="/article/%s">%s</a> -- <span class="date">%s</span> </li>`,
			arti.FileName, arti.Title, arti.Date.Format("Jan 2, 2006")))

	}

	rssBuilder.WriteString(`
      </channel>
    </rss>
    `)

	bm.articleMutex.Lock()
	bm.Articles = newArticles
	bm.HTMLList = []byte(strings.Join(links, "<br/>"))
	bm.RSSFeed = []byte(rssBuilder.String())
	bm.articleMutex.Unlock()

	managerLogger.Info().Msgf("content update succedeed: loaded %d articles", len(newArticles))
	return nil
}
