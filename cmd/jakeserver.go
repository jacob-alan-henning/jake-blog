package main

import (
	"fmt"
	"jakeblog/internal/blog"
)

func main() {
	initZLOG(INFO)

	err := blog.StartBlogServer()
	if err != nil {
		initLogMSG(ERROR, fmt.Sprintf("blogserver stopped with error: %v", err))
	}
}
