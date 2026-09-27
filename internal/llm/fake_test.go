package llm

import (
	"context"
	"testing"
)

func TestFake(t *testing.T) {
	f := &Fake{Replies: []string{"first", "second"}}

	req1 := Request{Prompt: "one"}
	resp, err := f.Complete(context.Background(), req1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "first" {
		t.Fatalf("resp.Text = %q, want %q", resp.Text, "first")
	}

	req2 := Request{Prompt: "two"}
	resp, err = f.Complete(context.Background(), req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "second" {
		t.Fatalf("resp.Text = %q, want %q", resp.Text, "second")
	}

	if len(f.Calls) != 2 || f.Calls[0] != req1 || f.Calls[1] != req2 {
		t.Fatalf("Calls = %+v, want [%+v %+v]", f.Calls, req1, req2)
	}

	if _, err := f.Complete(context.Background(), Request{Prompt: "three"}); err == nil {
		t.Fatal("want error once Replies is exhausted")
	} else if err.Error() != "llm: fake has no more replies" {
		t.Fatalf("unexpected error message: %v", err)
	}

	if f.Name() != "fake" {
		t.Fatalf("Name() = %q, want %q", f.Name(), "fake")
	}
}
