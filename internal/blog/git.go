package blog

import (
	//	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	// "go.opentelemetry.io/otel/attribute"
	// "go.opentelemetry.io/otel/codes"
	// "go.opentelemetry.io/otel/trace"
)

func getFileLastModified(repoPath string, filePath string) (time.Time, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return time.Time{}, err
	}

	ref, err := repo.Head()
	if err != nil {
		return time.Time{}, err
	}

	// Get commit history
	cIter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return time.Time{}, err
	}

	var latestCommitDate time.Time
	err = cIter.ForEach(func(c *object.Commit) error {
		// Get the files changed in this commit
		files, err := c.Files()
		if err != nil {
			return err
		}

		// Check if our target file was changed
		files.ForEach(func(f *object.File) error {
			if f.Name == filePath {
				latestCommitDate = c.Author.When
			}
			return nil
		})

		return nil
	})

	if err != nil {
		return time.Time{}, err
	}

	if latestCommitDate.IsZero() {
		return time.Time{}, fmt.Errorf("file not found in git history")
	}

	return latestCommitDate, nil
}

func FetchMarkdownRepo(cfg *Config) error {
	//_, span := tracer.Start(ctx, "pull md repo")
	//defer span.End()
	sshAuth, err := ssh.NewPublicKeysFromFile("git", cfg.KeyPrivPath, cfg.RepoPass)
	if err != nil {
		fmt.Fprintf(os.Stdout, "Error loading SSH keys: %v\n", err)
		//span.SetStatus(codes.Error, err.Error())
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
			//span.SetAttributes(
			//	attribute.Bool("repo.exists", true),
			//)

			repo, err := git.PlainOpen(cfg.ContentDir)
			if err != nil {
				//span.SetStatus(codes.Error, err.Error())
				return err
			}

			worktree, err := repo.Worktree()
			if err != nil {
				//span.SetStatus(codes.Error, err.Error())
				return err
			}
			err = worktree.Pull(&git.PullOptions{
				RemoteName: "origin",
				Auth:       sshAuth,
			})
			if err != nil && err != git.NoErrAlreadyUpToDate {
				log.Printf("Failed to pull repo: %v", err)
				//span.SetStatus(codes.Error, err.Error())
				return err
			}

		} else {
			log.Printf("Error cloning repository: %v", err)
			//span.SetStatus(codes.Error, err.Error())
			return err
		}
	}
	log.Printf("Repository cloned successfully: %v\n", repo)
	//span.AddEvent("pulled md repo")
	return nil
}
