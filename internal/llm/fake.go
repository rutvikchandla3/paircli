package llm

import (
	"context"
	"errors"
)

// Fake is an in-memory Provider for tests. It never touches the network.
type Fake struct {
	Replies []string  // returned in order; error when exhausted
	Calls   []Request // every request Complete received, in order
}

// Name implements Provider.
func (f *Fake) Name() string { return "fake" }

// Complete implements Provider: it records req in Calls, then pops and
// returns the next reply from Replies. When Replies is exhausted it returns
// an error.
func (f *Fake) Complete(ctx context.Context, req Request) (Response, error) {
	f.Calls = append(f.Calls, req)
	if len(f.Replies) == 0 {
		return Response{}, errors.New("llm: fake has no more replies")
	}
	reply := f.Replies[0]
	f.Replies = f.Replies[1:]
	return Response{Text: reply}, nil
}
