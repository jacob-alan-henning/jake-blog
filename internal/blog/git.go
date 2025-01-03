package blog

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

func getFileLastModified(cfg *Config, filePath string) (time.Time, error) {
	if cfg.LocalOnly {
		return time.Now(), nil
	}

	repo, err := git.PlainOpen(cfg.ContentDir)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to open git repo: %w", err)
	}

	ref, err := repo.Head()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get repo HEAD: %w", err)
	}

	// Get commit history
	cIter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get commit history: %w", err)
	}

	var latestCommitDate time.Time
	err = cIter.ForEach(func(c *object.Commit) error {
		if c == nil {
			return fmt.Errorf("encountered nil commit")
		}

		// Get the files changed in this commit
		files, err := c.Files()
		if err != nil {
			return fmt.Errorf("failed to get files for commit %s: %w", c.Hash, err)
		}

		// Check if our target file was changed
		if err := files.ForEach(func(f *object.File) error {
			if f == nil {
				return fmt.Errorf("encountered nil file")
			}
			if f.Name == filePath {
				latestCommitDate = c.Author.When
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to iterate files in commit %s: %w", c.Hash, err)
		}

		return nil
	})

	if err != nil {
		return time.Time{}, fmt.Errorf("failed to iterate commits: %w", err)
	}

	if latestCommitDate.IsZero() {
		return time.Time{}, fmt.Errorf("file %s not found in git history", filePath)
	}

	return latestCommitDate, nil
}

func FetchMarkdownRepo(cfg *Config) error {
	if cfg.LocalOnly {
		log.Printf("LocalOnly == true no repo cloned")
		return nil
	}

	sshAuth, err := ssh.NewPublicKeysFromFile("git", cfg.KeyPrivPath, cfg.RepoPass)
	if err != nil {
		fmt.Fprintf(os.Stdout, "Error loading SSH keys: %v\n", err)

		return err
	}

	repo, err := git.PlainClone(cfg.ContentDir, false, &git.CloneOptions{
		URL:           cfg.RepoURL,
		ReferenceName: plumbing.ReferenceName("refs/heads/main"),
		Auth:          sshAuth,
	})
	if err != nil {
		if err == git.ErrRepositoryAlreadyExists {
			log.Println("Repo already exists, opening and pulling latest changes")

			repo, err := git.PlainOpen(cfg.ContentDir)
			if err != nil {
				return err
			}

			worktree, err := repo.Worktree()
			if err != nil {
				return err
			}
			err = worktree.Pull(&git.PullOptions{
				RemoteName: "origin",
				Auth:       sshAuth,
			})
			if err != nil && err != git.NoErrAlreadyUpToDate {
				log.Printf("Failed to pull repo: %v", err)
				return err
			}

		} else {
			log.Printf("Error cloning repository: %v", err)
			return err
		}
	}
	log.Printf("Repository cloned successfully: %v\n", repo)
	return nil
}
