package blog

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkMetricSnippet(b *testing.B) {
	contentDir := b.TempDir()
	keyPath := filepath.Join(b.TempDir(), "test-key")

	testContent := `# Test Article
This is a test article's content.`
	_ = os.WriteFile(filepath.Join(contentDir, "test.md"), []byte(testContent), 0644)

	_ = os.WriteFile(keyPath, []byte("dummy-key"), 0600)

	port, _ := getFreePort()

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

	bs, _ := NewBlogServer(
		WithConfig("TEST_"),
	)

	go func() {
		_ = bs.Start()
	}()

	b.Cleanup(func() {
		cancel()
		bs.shutdown()
		time.Sleep(time.Second)
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bs.server.makeMetricSnippet()
	}
}

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
