package main

import (
	"jakeblog/internal/blog"
	"log"
	"os"
	"runtime/pprof"
)

func main() {
	if blog.CheckAnonEnvironmentalFlag("ENABLE_PROFILING") {
		log.Print("starting blog with profiling enabled")
		f, err := os.Create(blog.CheckAnonEnvironmental("PROFILING_REPORT"))
		if err != nil {
			log.Fatal(err)
		}

		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()

		err = blog.StartBlogServer()
		if err != nil {
			log.Fatal(err)
		}
	} else {
		log.Print("starting blog with profiling disabled")
		err := blog.StartBlogServer()
		if err != nil {
			log.Fatal(err)
		}
	}
}
