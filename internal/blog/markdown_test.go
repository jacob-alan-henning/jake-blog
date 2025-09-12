package blog

import "testing"

func BenchmarkMarkdowntoHtml(b *testing.B) {
	md := `
  # header
	*item1
	*item2 
	*item3 
	## subheader
	`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = markdownToHtml(md, true)
	}
}
