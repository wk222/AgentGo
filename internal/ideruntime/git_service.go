package ideruntime

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitFileChange represents the change state of a specific file.
type GitFileChange struct {
	FilePath string `json:"file_path"`
	FileURI  string `json:"file_uri"`
	Staged   string `json:"staged"`   // M, A, D, R, C, or empty
	Unstaged string `json:"unstaged"` // M, D, ?, or empty
}

// GitStatusResult summarizes the state of the active git working tree.
type GitStatusResult struct {
	Branch       string          `json:"branch"`
	Upstream     string          `json:"upstream"`
	Ahead        int             `json:"ahead"`
	Behind       int             `json:"behind"`
	Staged       []GitFileChange `json:"staged"`
	Unstaged     []GitFileChange `json:"unstaged"`
	Untracked    []string        `json:"untracked"`
	HasConflicts bool            `json:"has_conflicts"`
}

// GitService handles Git operations through the system Git executable and ProcessSupervisor.
type GitService struct {
	supervisor *ProcessSupervisor
	fs         *WorkspaceFS
	gitPath    string
	initOnce   sync.Once
	initErr    error
}

// NewGitService creates a Git service for the workspace.
func NewGitService(supervisor *ProcessSupervisor, fs *WorkspaceFS) *GitService {
	return &GitService{
		supervisor: supervisor,
		fs:         fs,
	}
}

func (s *GitService) resolveGitBinary() (string, error) {
	s.initOnce.Do(func() {
		if p, err := exec.LookPath("git"); err == nil {
			s.gitPath = p
			return
		}
		if p, err := exec.LookPath("git.exe"); err == nil {
			s.gitPath = p
			return
		}
		s.initErr = errors.New("git binary not found in PATH")
	})
	return s.gitPath, s.initErr
}

// GetStatus parses git status --porcelain=v2 --branch.
func (s *GitService) GetStatus(ctx context.Context) (GitStatusResult, error) {
	git, err := s.resolveGitBinary()
	if err != nil {
		return GitStatusResult{}, err
	}

	root := s.fs.RootPath()
	spec := ProcessSpec{
		ID:      fmt.Sprintf("git-status-%d", time.Now().UnixNano()),
		Kind:    KindGit,
		Command: git,
		Args:    []string{"status", "--porcelain=v2", "--branch"},
		Cwd:     root,
	}

	var mu sync.Mutex
	var buf bytes.Buffer

	h, err := s.supervisor.Spawn(ctx, spec)
	if err != nil {
		return GitStatusResult{}, fmt.Errorf("spawn git status: %w", err)
	}

	h.OnOutput(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		buf.Write(data)
	})

	_ = h.Wait()

	mu.Lock()
	out := buf.String()
	mu.Unlock()

	res := GitStatusResult{
		Staged:    make([]GitFileChange, 0),
		Unstaged:  make([]GitFileChange, 0),
		Untracked: make([]string, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "# branch.head ") {
			res.Branch = strings.TrimPrefix(line, "# branch.head ")
		} else if strings.HasPrefix(line, "# branch.upstream ") {
			res.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		} else if strings.HasPrefix(line, "# branch.ab ") {
			parts := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(parts) >= 2 {
				ahead, _ := strconv.Atoi(strings.TrimPrefix(parts[0], "+"))
				behind, _ := strconv.Atoi(strings.TrimPrefix(parts[1], "-"))
				res.Ahead = ahead
				res.Behind = behind
			}
		} else if strings.HasPrefix(line, "? ") {
			// Untracked
			rel := strings.TrimPrefix(line, "? ")
			full := filepath.Join(root, rel)
			res.Untracked = append(res.Untracked, PathToURI(full))
		} else if strings.HasPrefix(line, "u ") {
			// Unmerged / conflict
			res.HasConflicts = true
			parts := strings.Fields(line)
			if len(parts) >= 11 {
				rel := parts[10]
				full := filepath.Join(root, rel)
				res.Unstaged = append(res.Unstaged, GitFileChange{
					FilePath: rel,
					FileURI:  PathToURI(full),
					Unstaged: "U",
				})
			}
		} else if strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 ") {
			// Ordinary or renamed entry
			// 1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
			parts := strings.Fields(line)
			if len(parts) >= 9 {
				xy := parts[1]
				rel := parts[8]
				full := filepath.Join(root, rel)
				uri := PathToURI(full)

				stagedChar := string(xy[0])
				unstagedChar := string(xy[1])

				if stagedChar != "." {
					res.Staged = append(res.Staged, GitFileChange{
						FilePath: rel,
						FileURI:  uri,
						Staged:   stagedChar,
					})
				}
				if unstagedChar != "." {
					res.Unstaged = append(res.Unstaged, GitFileChange{
						FilePath: rel,
						FileURI:  uri,
						Unstaged: unstagedChar,
					})
				}
			}
		}
	}

	return res, nil
}

// GetDiff retrieves the git diff patch for a file.
func (s *GitService) GetDiff(ctx context.Context, fileURI string, staged bool) (string, error) {
	git, err := s.resolveGitBinary()
	if err != nil {
		return "", err
	}

	localPath, err := s.fs.ResolveURI(fileURI)
	if err != nil {
		return "", err
	}

	root := s.fs.RootPath()
	rel, _ := filepath.Rel(root, localPath)

	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", rel)

	spec := ProcessSpec{
		ID:      fmt.Sprintf("git-diff-%d", time.Now().UnixNano()),
		Kind:    KindGit,
		Command: git,
		Args:    args,
		Cwd:     root,
	}

	var mu sync.Mutex
	var buf bytes.Buffer

	h, err := s.supervisor.Spawn(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("spawn git diff: %w", err)
	}

	h.OnOutput(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		buf.Write(data)
	})

	_ = h.Wait()

	mu.Lock()
	diff := buf.String()
	mu.Unlock()

	return diff, nil
}

// Stage adds paths to Git index.
func (s *GitService) Stage(ctx context.Context, fileURIs ...string) error {
	return s.runGitCmd(ctx, "add", fileURIs...)
}

// Unstage removes paths from Git index.
func (s *GitService) Unstage(ctx context.Context, fileURIs ...string) error {
	return s.runGitCmd(ctx, "reset", fileURIs...)
}

// Commit creates a new commit with the given message.
func (s *GitService) Commit(ctx context.Context, message string) error {
	git, err := s.resolveGitBinary()
	if err != nil {
		return err
	}

	root := s.fs.RootPath()
	spec := ProcessSpec{
		ID:      fmt.Sprintf("git-commit-%d", time.Now().UnixNano()),
		Kind:    KindGit,
		Command: git,
		Args:    []string{"commit", "-m", message},
		Cwd:     root,
	}

	h, err := s.supervisor.Spawn(ctx, spec)
	if err != nil {
		return err
	}
	if err := h.Wait(); err != nil {
		return err
	}
	if h.ExitCode() != 0 {
		return fmt.Errorf("git commit exit code %d", h.ExitCode())
	}
	return nil
}

func (s *GitService) runGitCmd(ctx context.Context, subCmd string, fileURIs ...string) error {
	git, err := s.resolveGitBinary()
	if err != nil {
		return err
	}

	root := s.fs.RootPath()
	args := []string{subCmd, "--"}

	for _, u := range fileURIs {
		p, err := s.fs.ResolveURI(u)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		args = append(args, rel)
	}

	spec := ProcessSpec{
		ID:      fmt.Sprintf("git-%s-%d", subCmd, time.Now().UnixNano()),
		Kind:    KindGit,
		Command: git,
		Args:    args,
		Cwd:     root,
	}

	h, err := s.supervisor.Spawn(ctx, spec)
	if err != nil {
		return err
	}
	if err := h.Wait(); err != nil {
		return err
	}
	if h.ExitCode() != 0 {
		return fmt.Errorf("git %s exit code %d", subCmd, h.ExitCode())
	}
	return nil
}
