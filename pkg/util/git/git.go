package git

import (
	"fmt"
	"os"

	"github.com/pkg/errors"
	git "gopkg.in/src-d/go-git.v4"
	plumbing "gopkg.in/src-d/go-git.v4/plumbing"
)

// Repository holds the information about a repository
type Repository struct {
	LocalPath string
	RemotURL  string
}

// NewGitRepository creates a new git repository struct with the given parameters
func NewGitRepository(localPath string, remoteURL string) *Repository {
	return &Repository{
		LocalPath: localPath,
		RemotURL:  remoteURL,
	}
}

// GetHash retrieves the current HEADs hash
func (gr *Repository) GetHash() (string, error) {
	repo, err := git.PlainOpen(gr.LocalPath)
	if err != nil {
		return "", errors.Wrap(err, "git open")
	}

	head, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "get head")
	}

	return head.Hash().String(), nil
}

// GetRemote retrieves the remote origin
func (gr *Repository) GetRemote() (string, error) {
	_, err := os.Stat(gr.LocalPath + "/.git")
	if err != nil {
		return "", err
	}

	repo, err := git.PlainOpen(gr.LocalPath)
	if err != nil {
		return "", errors.Wrap(err, "git open")
	}

	remotes, err := repo.Remotes()
	if err != nil {
		return "", errors.Wrap(err, "get remotes")
	}

	if len(remotes) == 0 {
		return "", fmt.Errorf("Couldn't determine git remote in %s", gr.LocalPath)
	}

	urls := remotes[0].Config().URLs
	if len(urls) == 0 {
		return "", errors.New("No remotes found")
	}

	return urls[0], nil
}

// Update pulls the repository or clones it into the local path
func (gr *Repository) Update(merge bool) error {
	// Check if repo already exists
	_, err := os.Stat(gr.LocalPath + "/.git")
	if err != nil {
		// Create local path
		err := os.MkdirAll(gr.LocalPath, 0755)
		if err != nil {
			return err
		}

		// Clone into folder
		_, err = git.PlainClone(gr.LocalPath, false, &git.CloneOptions{
			URL: gr.RemotURL,
		})
		if err != nil {
			return err
		}

		return nil
	}

	// Open existing repo
	repo, err := git.PlainOpen(gr.LocalPath)
	if err != nil {
		return err
	}

	// Pull or fetch?
	if merge {
		repoWorktree, err := repo.Worktree()
		if err != nil {
			return err
		}

		err = repoWorktree.Pull(&git.PullOptions{
			RemoteName: "origin",
		})
		if err != git.NoErrAlreadyUpToDate && err != nil {
			return err
		}
	} else {
		err = repo.Fetch(&git.FetchOptions{
			RemoteName: "origin",
		})
		if err != git.NoErrAlreadyUpToDate && err != nil {
			return err
		}
	}

	return nil
}

// Checkout certain tag, branch or hash
func (gr *Repository) Checkout(tag, branch, revision string) error {
	r, err := git.PlainOpen(gr.LocalPath)
	if err != nil {
		return err
	}

	// Resolve to the correct hash
	var hash *plumbing.Hash
	if tag != "" {
		hash, err = r.ResolveRevision(plumbing.Revision(fmt.Sprintf("refs/tags/%s", tag)))
		if err != nil {
			return fmt.Errorf("Error resolving tag revision: %v", err)
		}
	} else if branch != "" {
		hash, err = r.ResolveRevision(plumbing.Revision(fmt.Sprintf("refs/remotes/origin/%s", branch)))
		if err != nil {
			return fmt.Errorf("Error resolving branch revision: %v", err)
		}
	} else if revision != "" {
		h := plumbing.NewHash(revision)
		hash = &h
	} else {
		return errors.New("Tag, branch or hash has to be defined")
	}

	// Checkout the hash
	w, err := r.Worktree()
	if err != nil {
		return err
	}

	return w.Checkout(&git.CheckoutOptions{
		Hash: *hash,
	})
}
