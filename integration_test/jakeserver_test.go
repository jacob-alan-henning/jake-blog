package integration_test

import (
	"context"
	"fmt"
	"io"
	"jakeblog/internal/blog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func getFreePort() (string, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "", err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", err
	}
	defer l.Close()
	return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port), nil
}

func TestBlogServerIntegration(t *testing.T) {
	// create a temp dir for our test content
	contentDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "test-key")

	// create a test markdown file in the correct location
	testContent := `# Test Article
This is a test article's content.`
	err := os.WriteFile(filepath.Join(contentDir, "test.md"), []byte(testContent), 0644)
	require.NoError(t, err)

	// create dummy SSH key
	err = os.WriteFile(keyPath, []byte("dummy-key"), 0600)
	require.NoError(t, err)

	port, err := getFreePort()
	require.NoError(t, err)

	// set up test environment
	os.Setenv("TEST_LOCAL_ONLY", "true")
	os.Setenv("TEST_SERVER_PORT", port)
	os.Setenv("TEST_CONTENT_DIR", contentDir)
	os.Setenv("TEST_REPO_URL", "dummy-url")
	os.Setenv("TEST_REPO_PRIV_KEY", "dummy-key")
	os.Setenv("TEST_REPO_PRIV_KEY_PATH", keyPath)
	os.Setenv("TEST_ENVMNT", "test")

	defer func() {
		os.Unsetenv("TEST_LOCAL_ONLY")
		os.Unsetenv("TEST_SERVER_PORT")
		os.Unsetenv("TEST_CONTENT_DIR")
		os.Unsetenv("TEST_REPO_URL")
		os.Unsetenv("TEST_REPO_PRIV_KEY")
		os.Unsetenv("TEST_REPO_PRIV_KEY_PATH")
		os.Unsetenv("TEST_ENVMNT")
	}()

	_, cancel := context.WithCancel(context.Background())

	bs, err := blog.NewBlogServer(
		blog.WithConfig("TEST_"),
	)
	require.NoError(t, err)

	// start server in bg
	go func() {
		if err := bs.Start(); err != nil {
			t.Errorf("server error: %v", err)
		}
	}()

	// ensure cleanup
	t.Cleanup(func() {
		cancel()
		time.Sleep(time.Second)
	})

	client := &http.Client{Timeout: 1 * time.Second}
	serverURL := fmt.Sprintf("http://localhost:%s", port)

	var resp *http.Response
	startTime := time.Now()
	for time.Since(startTime) < 5*time.Second {
		resp, err = client.Get(serverURL + "/content/")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && len(body) > 0 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, err, "server failed to start and serve content")

	// Test cases
	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "article list", // verify articles are listed correctly
			path:           "/content/",
			expectedStatus: http.StatusOK,
			expectedBody:   "Test Article",
		},
		{
			name:           "metrics endpoint", // verify correct response from metrics endpoint
			path:           "/telemetry/metric",
			expectedStatus: http.StatusOK,
			expectedBody:   "blog.articles.served: 0",
		},
		{
			name:           "metrics endpoint p50", // verify correct response from metrics endpoint
			path:           "/telemetry/metric",
			expectedStatus: http.StatusOK,
			expectedBody:   "<p>blog.server.request.ms.p50:",
		},
		{
			name:           "metrics endpoint p90", // verify correct response from metrics endpoint
			path:           "/telemetry/metric",
			expectedStatus: http.StatusOK,
			expectedBody:   "<p>blog.server.request.ms.p90:",
		},
		{
			name:           "metrics endpoint p95", // verify correct response from metrics endpoint
			path:           "/telemetry/metric",
			expectedStatus: http.StatusOK,
			expectedBody:   "<p>blog.server.request.ms.p95:",
		},
		{
			name:           "metrics endpoint p99", // verify correct response from metrics endpoint
			path:           "/telemetry/metric",
			expectedStatus: http.StatusOK,
			expectedBody:   "<p>blog.server.request.ms.p99:",
		},
		{
			name:           "article content", // verify correct response from real article
			path:           "/article/test",
			expectedStatus: http.StatusOK,
			expectedBody:   "This is a test article",
		},
		{
			name:           "non-existent article", // verify correct response from missing article
			path:           "/article/doesnotexist",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "Path traversal attempt", // verify users cannot use path traversal
			path:           "/article/../../../etc/passwd",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(serverURL + tt.path)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.expectedBody != "" {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Contains(t, string(body), tt.expectedBody)
			}
		})
	}

	// verify metrics update after viewing article
	t.Run("metrics update after view", func(t *testing.T) {
		time.Sleep(6000 * time.Millisecond) // 6 seconds to ensure we catch the export

		resp, err = client.Get(serverURL + "/telemetry/metric")
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "blog.articles.served: 1")
	})
}
