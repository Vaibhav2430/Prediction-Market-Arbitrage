package main

import (
	"context"
	"io"
	"testing"
)

func TestInvalidFlagsAndHelp(t *testing.T) {
	for _, args := range [][]string{{"-capture"}, {"-quantity", "0"}, {"-timeout", "0s"}, {"-polymarket-id", "123"}, {"extra"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}
