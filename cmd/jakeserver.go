package main

import (
	"jakeblog/internal/blog"
	"log"
)

func main() {
	bs, err := blog.NewBlogServer(
		blog.WithConfig("BLOG_"),
	)
	if err != nil {
		log.Fatal(err)
	}

	err = bs.Start()
	if err != nil {
		log.Fatal(err)
	}

}
