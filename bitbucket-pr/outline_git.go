package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func gitCommitAvailable(repoDir, hash string) error {
	if repoDir == "" {
		return errors.New("ローカルcheckoutが関連付けられていません")
	}
	if hash == "" {
		return errors.New("PRのcommit hashがありません")
	}
	cmd := exec.Command("git", "-C", repoDir, "cat-file", "-e", hash+"^{commit}")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("commit %.12s がローカルにありません: %s", hash, msg)
	}
	return nil
}

func gitTreePaths(repoDir, hash string) ([]string, error) {
	out, err := exec.Command("git", "-C", repoDir, "ls-tree", "-r", "-z", "--name-only", hash).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %.12s: %w", hash, err)
	}
	parts := strings.Split(string(out), "\x00")
	paths := make([]string, 0, len(parts))
	for _, path := range parts {
		if path != "" {
			paths = append(paths, filepath.ToSlash(path))
		}
	}
	return paths, nil
}

// gitReadFiles uses one cat-file process for a snapshot batch. This avoids a
// git subprocess per source file, the dominant cost of naïve repository-wide
// analyzers.
func gitReadFiles(repoDir, hash string, paths []string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(paths))
	if len(paths) == 0 {
		return result, nil
	}
	cmd := exec.Command("git", "-C", repoDir, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start git cat-file: %w", err)
	}
	writeErr := make(chan error, 1)
	go func() {
		bw := bufio.NewWriter(stdin)
		for _, path := range paths {
			if _, err := fmt.Fprintf(bw, "%s:%s\n", hash, path); err != nil {
				writeErr <- err
				stdin.Close()
				return
			}
		}
		err := bw.Flush()
		closeErr := stdin.Close()
		if err == nil {
			err = closeErr
		}
		writeErr <- err
	}()

	br := bufio.NewReader(stdout)
	for _, path := range paths {
		header, err := br.ReadString('\n')
		if err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("read git cat-file header for %s: %w", path, err)
		}
		header = strings.TrimSpace(header)
		if strings.HasSuffix(header, " missing") {
			cmd.Wait()
			return nil, fmt.Errorf("%s:%s is missing", hash, path)
		}
		parts := strings.Fields(header)
		if len(parts) != 3 || parts[1] != "blob" {
			cmd.Wait()
			return nil, fmt.Errorf("unexpected git cat-file header for %s: %s", path, header)
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil || size < 0 {
			cmd.Wait()
			return nil, fmt.Errorf("invalid git object size for %s: %s", path, parts[2])
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(br, data); err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("read git blob %s: %w", path, err)
		}
		if b, err := br.ReadByte(); err != nil || b != '\n' {
			cmd.Wait()
			return nil, fmt.Errorf("git cat-file delimiter missing after %s", path)
		}
		result[path] = data
	}
	if err := <-writeErr; err != nil {
		cmd.Wait()
		return nil, fmt.Errorf("write git cat-file input: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return result, nil
}

// gitCandidatePaths uses git's snapshot-aware fixed-string search as a cheap
// prefilter. The parser only sees files that may define or reference a name
// involved in the changed symbols.
func gitCandidatePaths(repoDir, hash string, names []string) ([]string, error) {
	seen := map[string]bool{}
	for start := 0; start < len(names); start += 80 {
		end := min(start+80, len(names))
		args := []string{"-C", repoDir, "grep", "-I", "-l", "-F", "--full-name"}
		for _, name := range names[start:end] {
			args = append(args, "-e", name)
		}
		args = append(args, hash, "--")
		cmd := exec.Command("git", args...)
		out, err := cmd.Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				continue // no matches in this batch
			}
			return nil, fmt.Errorf("git grep %.12s: %w", hash, err)
		}
		prefix := hash + ":"
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, prefix)
			if _, ok := outlineLanguageForPath(line); ok {
				seen[filepath.ToSlash(line)] = true
			}
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}
