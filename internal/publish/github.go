package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const api = "https://api.github.com"

type Client struct {
	http   *http.Client
	token  string
	branch string
	base   string
	path   string // "" or "docs"
}

func New(token, branch, path string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 30 * time.Second},
		base:   api,
		token:  token,
		branch: branch,
		path:   strings.Trim(path, "/"),
	}
}

// Publish writes files to repo in a single commit.
//
// With force=true the commit has no parents and the branch ref is force-updated,
// so the repository never grows beyond one commit and old page versions (which
// contain people's activity) do not linger in history. Everything else in the
// repository survives because the tree is built on top of the current one.
func (c *Client) Publish(ctx context.Context, repo string, files map[string][]byte, message string, force bool) (string, error) {
	parent, baseTree, err := c.head(ctx, repo)
	if err != nil {
		return "", err
	}

	type entry struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	var entries []entry
	for name, data := range files {
		var blob struct {
			SHA string `json:"sha"`
		}
		if err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/git/blobs",
			map[string]string{"content": string(data), "encoding": "utf-8"}, &blob); err != nil {
			return "", fmt.Errorf("%s: blob %s: %w", repo, name, err)
		}
		p := name
		if c.path != "" {
			p = c.path + "/" + name
		}
		entries = append(entries, entry{Path: p, Mode: "100644", Type: "blob", SHA: blob.SHA})
	}

	treeReq := map[string]any{"tree": entries}
	if baseTree != "" {
		treeReq["base_tree"] = baseTree
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/git/trees", treeReq, &tree); err != nil {
		return "", fmt.Errorf("%s: tree: %w", repo, err)
	}

	commitReq := map[string]any{"message": message, "tree": tree.SHA, "parents": []string{}}
	if parent != "" && !force {
		commitReq["parents"] = []string{parent}
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/git/commits", commitReq, &commit); err != nil {
		return "", fmt.Errorf("%s: commit: %w", repo, err)
	}

	if parent == "" {
		err = c.do(ctx, http.MethodPost, "/repos/"+repo+"/git/refs",
			map[string]string{"ref": "refs/heads/" + c.branch, "sha": commit.SHA}, nil)
	} else {
		err = c.do(ctx, http.MethodPatch, "/repos/"+repo+"/git/refs/heads/"+c.branch,
			map[string]any{"sha": commit.SHA, "force": force}, nil)
	}
	if err != nil {
		return "", fmt.Errorf("%s: ref: %w", repo, err)
	}
	return commit.SHA, nil
}

// head returns the current commit and tree of the branch. Both empty means the
// branch does not exist yet (repo created but never pushed).
func (c *Client) head(ctx context.Context, repo string) (commit, tree string, err error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err = c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/ref/heads/"+c.branch, nil, &ref)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("%s: ref: %w", repo, err)
	}
	var cm struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/commits/"+ref.Object.SHA, nil, &cm); err != nil {
		return "", "", fmt.Errorf("%s: commit: %w", repo, err)
	}
	return ref.Object.SHA, cm.Tree.SHA, nil
}

type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("github %d: %s", e.code, strings.TrimSpace(e.body))
}

func isStatus(err error, code int) bool {
	se, ok := err.(*statusError)
	return ok && se.code == code
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "discord-stats")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &statusError{code: resp.StatusCode, body: string(b)}
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
