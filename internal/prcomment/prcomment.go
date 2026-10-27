// Package prcomment keeps one Pair review-link comment on a pull request. The
// comment is owned by the user running paircli: only a comment they wrote that
// carries Marker is ever edited, so nobody else's comment is touched and two
// reviewers each keep their own.
package prcomment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/pr"
)

// Marker identifies a Pair review-link comment.
const Marker = "<!-- pair-review-link -->"

// Body is the comment text for one review.
func Body(pairURL string) string {
	return "Review this PR in Pair: " + pairURL + "\n\n" + Marker
}

// Result says what Upsert did.
type Result string

const (
	Created   Result = "created"
	Updated   Result = "updated"
	Unchanged Result = "unchanged"
)

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Upsert creates the link comment, or updates the one the current gh user
// already posted. It reads the whole comment list (every page).
func Upsert(r pr.Runner, owner, repo string, number int, pairURL string) (Result, error) {
	if r == nil {
		return "", errors.New("prcomment: no gh runner")
	}
	out, err := r.Run("api", "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("prcomment: reading the gh user: %w", err)
	}
	viewer := strings.TrimSpace(string(out))
	if viewer == "" {
		return "", errors.New("prcomment: gh returned no user login")
	}

	listed, err := r.Run("api", "--paginate", fmt.Sprintf("repos/%s/%s/issues/%d/comments?per_page=100", owner, repo, number))
	if err != nil {
		return "", fmt.Errorf("prcomment: listing comments: %w", err)
	}
	existing, err := ownedComment(listed, viewer)
	if err != nil {
		return "", err
	}

	body := Body(pairURL)
	if existing == nil {
		if _, err := r.Run("api", "--method", "POST", fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, repo, number), "-f", "body="+body); err != nil {
			return "", fmt.Errorf("prcomment: creating the comment: %w", err)
		}
		return Created, nil
	}
	if strings.TrimSpace(existing.Body) == body {
		return Unchanged, nil
	}
	if _, err := r.Run("api", "--method", "PATCH", "repos/"+owner+"/"+repo+"/issues/comments/"+strconv.FormatInt(existing.ID, 10), "-f", "body="+body); err != nil {
		return "", fmt.Errorf("prcomment: updating comment %d: %w", existing.ID, err)
	}
	return Updated, nil
}

// ownedComment returns the first comment by viewer that carries Marker.
// `gh api --paginate` prints one JSON array per page back to back.
func ownedComment(listed []byte, viewer string) (*comment, error) {
	dec := json.NewDecoder(strings.NewReader(string(listed)))
	for {
		var page []comment
		if err := dec.Decode(&page); err == io.EOF {
			return nil, nil
		} else if err != nil {
			return nil, fmt.Errorf("prcomment: parsing the comment list: %w", err)
		}
		for i := range page {
			c := page[i]
			if c.ID > 0 && strings.Contains(c.Body, Marker) && strings.EqualFold(c.User.Login, viewer) {
				return &c, nil
			}
		}
	}
}
