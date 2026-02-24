package blog

import (
	"os"
	"path/filepath"
	"testing"
)

var markdownSink string

func BenchmarkMarkdowntoHtml(b *testing.B) {
	content := `# Testing and Deploying my Personal Blog

## Initial Speed Bump

Off to a rough start here. The morning after I did the initial manual deploy; I discovered that my blog was timing out when I tried to connect. The culprit here was the bundle id I had chosen. I had picked the nano_3_0 lightsail bundle; which is the smallest and cheapest option. Turns out that paticular instance burns cpu credits when cpu utilization is over 5 percent and the blog was using around 5.1 percent of the cpu.

## Testing the code

I am not someone who writes a lot of tests frequently. I understand the value but it is just not something I do a lot.

Testing the blogs code is a little tricky; an integration test proved more valuable from unit tests. The core functionality of this blog is not complex business logic or advanced algorithims(for now). The real risk is the plumbing I wrote not working with the individual components.

I decided to start with a integration test; an integration test verifies that all the components in my blog work together.

The next step was to fuzz my blog. Fuzzing is throwing random garbage at the blog untill it exhibits unexpected behavior.

## Deployment

I originally set the server up to only allow ssh connections from my personal IP and that doesn't feel like a great solution.

` + "```" + `
- block:
    - name: Get current public IP
      ansible.builtin.uri:
        url: https://api.ipify.org?format=json
        return_content: yes
      register: public_ip
      delegate_to: localhost
      changed_when: false

    - name: Open SSH port for current IP
      ansible.builtin.command: >-
        aws lightsail open-instance-public-ports
        --instance-name {{ instance_name }}
        --port-info fromPort=22,toPort=22,protocol=TCP
      delegate_to: localhost
` + "```" + `

## CICD

Github actions is new for me. I prefer gitlab approach of using an arbitrary container.

## Roadmap

* Add RSS feed
* Add custom markdown parser
* Benchmark and optimize the blog
* setup some real alerts

## Emerging architecture
`

	mdPath := filepath.Join(b.TempDir(), "test-article.md")
	if err := os.WriteFile(mdPath, []byte(content), 0644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := markdownToHTML(mdPath, false)
		if err != nil {
			b.Fatal(err)
		}
		markdownSink = result
	}
}
