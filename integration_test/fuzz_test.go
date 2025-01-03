package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jakeblog/internal/blog"

	"github.com/stretchr/testify/require"
)

var (
	serverURL string
	setupOnce sync.Once
)

func FuzzBlogServer(f *testing.F) {
	setupOnce.Do(func() {
		contentDir := f.TempDir()
		keyPath := filepath.Join(f.TempDir(), "test-key")

		testContent := `# Test Article
This is a test article's content.`
		err := os.WriteFile(filepath.Join(contentDir, "test.md"), []byte(testContent), 0644)
		require.NoError(f, err)

		err = os.WriteFile(keyPath, []byte("dummy-key"), 0600)
		require.NoError(f, err)

		port, err := getFreePort()
		require.NoError(f, err)

		os.Setenv("TEST_LOCAL_ONLY", "true")
		os.Setenv("TEST_SERVER_PORT", port)
		os.Setenv("TEST_CONTENT_DIR", contentDir)
		os.Setenv("TEST_REPO_URL", "dummy-url")
		os.Setenv("TEST_REPO_PRIV_KEY", "dummy-key")
		os.Setenv("TEST_REPO_PRIV_KEY_PATH", keyPath)

		ctx, cancel := context.WithCancel(context.Background())

		bs, err := blog.NewBlogServer(
			blog.WithConfig("TEST_"),
		)
		require.NoError(f, err)

		go func() {
			if err := bs.Start(); err != nil && ctx.Err() == nil {
				f.Errorf("server error: %v", err)
			}
		}()

		f.Cleanup(func() {
			cancel()
			os.Unsetenv("TEST_LOCAL_ONLY")
			os.Unsetenv("TEST_SERVER_PORT")
			os.Unsetenv("TEST_CONTENT_DIR")
			os.Unsetenv("TEST_REPO_URL")
			os.Unsetenv("TEST_REPO_PRIV_KEY")
			os.Unsetenv("TEST_REPO_PRIV_KEY_PATH")
			time.Sleep(time.Second)
		})

		serverURL = fmt.Sprintf("http://localhost:%s", port)

		// wait for server to be ready
		client := &http.Client{Timeout: 1 * time.Second}
		startTime := time.Now()
		for time.Since(startTime) < 5*time.Second {
			resp, err := client.Get(serverURL + "/content/")
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK && len(body) > 0 {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	// fuzz seeds
	f.Add("/article/test")
	f.Add("/content/")
	f.Add("/article/../../../etc/passwd")
	f.Add("/article/<script>alert(1)</script>")
	f.Add("/article/doesnotexist")

	// actual fuzz test
	f.Fuzz(func(t *testing.T, path string) {
		client := &http.Client{Timeout: 1 * time.Second}

		resp, err := client.Get(serverURL + path)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		if strings.HasPrefix(path, "@") {
			if resp.StatusCode != http.StatusConflict && // 409 auth info is malformed
				resp.StatusCode != http.StatusForbidden { // 403 auth failed
				t.Errorf("Unexpected status code: %d for @ path: %s", resp.StatusCode, path)
			}
			return
		}

		// verify the server handles all input gracefully
		if resp.StatusCode != http.StatusOK &&
			resp.StatusCode != http.StatusNotFound &&
			resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Unexpected status code: %d for path: %s", resp.StatusCode, path)
		}
	})
}
